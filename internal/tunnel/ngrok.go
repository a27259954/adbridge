package tunnel

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.ngrok.com/ngrok/v2"

	"github.com/a27259954/adbridge/internal/config"
)

func startNgrok(ctx context.Context, cfg config.Tunnel, h http.Handler, progress func(string)) (*session, error) {
	if strings.TrimSpace(cfg.NgrokAuthtoken) == "" {
		return nil, errors.New("ngrok needs an authtoken: create a free account at dashboard.ngrok.com, copy the authtoken into Settings")
	}
	progress("connecting to ngrok")
	agent, err := ngrok.NewAgent(
		ngrok.WithAuthtoken(strings.TrimSpace(cfg.NgrokAuthtoken)),
		ngrok.WithAgentDescription("adbridge"),
	)
	if err != nil {
		return nil, err
	}
	opts := []ngrok.EndpointOption{ngrok.WithDescription("adbridge MCP")}
	if d := strings.TrimSpace(cfg.NgrokDomain); d != "" {
		d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
		opts = append(opts, ngrok.WithURL("https://"+strings.TrimSuffix(d, "/")))
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ln, err := agent.Listen(lctx, opts...)
	if err != nil {
		_ = agent.Disconnect()
		return nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &session{
		url: ln.URL().String(),
		stop: func() {
			_ = srv.Close()
			_ = ln.Close()
			_ = agent.Disconnect()
		},
	}, nil
}
