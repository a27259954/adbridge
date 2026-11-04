package tunnel

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/config"
	"github.com/a27259954/adbridge/internal/paths"
)

const (
	cfRelease = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"
	cfAsset   = "cloudflared-windows-amd64.exe"
)

var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func cloudflaredPath() string { return filepath.Join(paths.BinDir(), "cloudflared.exe") }

func startCloudflared(ctx context.Context, _ config.Tunnel, h http.Handler, progress func(string)) (*session, error) {
	bin := cloudflaredPath()
	if _, err := os.Stat(bin); err != nil {
		progress("downloading cloudflared")
		if err := downloadCloudflared(ctx, bin); err != nil {
			return nil, fmt.Errorf("download cloudflared: %w", err)
		}
	}

	// A private loopback listener serves the *remote* handler, so requests
	// arriving through cloudflared get remote auth and permissions even
	// though they reach us from 127.0.0.1.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	progress("starting quick tunnel")
	pctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(pctx, bin, "tunnel", "--no-autoupdate", "--url", "http://"+ln.Addr().String())
	adb.HideWindow(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		srv.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		srv.Close()
		return nil, err
	}
	stop := func() {
		cancel()
		_ = cmd.Wait()
		_ = srv.Close()
	}
	urlCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if u := trycloudflareRe.FindString(sc.Text()); u != "" {
				select {
				case urlCh <- u:
				default:
				}
			}
		}
	}()
	select {
	case u := <-urlCh:
		return &session{url: u, stop: stop}, nil
	case <-ctx.Done():
		stop()
		return nil, ctx.Err()
	case <-time.After(45 * time.Second):
		stop()
		return nil, errors.New("cloudflared did not report a tunnel URL within 45s")
	}
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"` // "sha256:<hex>"
	} `json:"assets"`
}

func downloadCloudflared(ctx context.Context, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfRelease, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "adbridge")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return err
	}
	var url, want string
	for _, a := range rel.Assets {
		if a.Name == cfAsset {
			url, want = a.URL, strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	if url == "" {
		return fmt.Errorf("%s not found in release %s", cfAsset, rel.TagName)
	}
	if len(want) != 64 {
		return errors.New("release asset has no SHA-256 digest; refusing to run an unverified binary")
	}
	if !strings.HasPrefix(url, "https://github.com/cloudflare/cloudflared/") {
		return fmt.Errorf("unexpected download URL %s", url)
	}

	dreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	dreq.Header.Set("User-Agent", "adbridge")
	dresp, err := http.DefaultClient.Do(dreq)
	if err != nil {
		return err
	}
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", dresp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, hash), io.LimitReader(dresp.Body, 200<<20))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, want) {
		os.Remove(tmp)
		return fmt.Errorf("SHA-256 mismatch: got %s want %s", got, want)
	}
	return os.Rename(tmp, dst)
}
