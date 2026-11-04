package adb

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
)

// ErrNotFound means no adb executable could be located.
var ErrNotFound = errors.New("adb.exe not found: install Android SDK Platform-Tools and add it to PATH, or set the path in Settings")

func exeName() string {
	if runtime.GOOS == "windows" {
		return "adb.exe"
	}
	return "adb"
}

// Locate finds adb. Search order: override, PATH, ANDROID_HOME,
// ANDROID_SDK_ROOT, the Android Studio default SDK location.
func Locate(override string) (string, error) {
	if override != "" {
		if isFile(override) {
			return override, nil
		}
		return "", errors.New("configured adb path does not exist: " + override)
	}
	if p, err := exec.LookPath("adb"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
		return p, nil
	}
	var candidates []string
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" {
			candidates = append(candidates, filepath.Join(v, "platform-tools", exeName()))
		}
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		candidates = append(candidates, filepath.Join(v, "Android", "Sdk", "platform-tools", exeName()))
	}
	for _, c := range candidates {
		if isFile(c) {
			return c, nil
		}
	}
	return "", ErrNotFound
}

var versionRe = regexp.MustCompile(`(?m)^Version\s+(\S+)`)

// BinaryVersion returns the platform-tools version of bin (e.g. "37.0.1-15733141").
func BinaryVersion(ctx context.Context, bin string) (string, error) {
	out, err := New(bin).run(ctx, "version")
	if err != nil {
		return "", err
	}
	if m := versionRe.FindStringSubmatch(out); m != nil {
		return m[1], nil
	}
	return "", errors.New("unrecognised `adb version` output")
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
