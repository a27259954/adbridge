// Package tunnel publishes the MCP endpoint on the internet so cloud AI
// agents that cannot install anything can still reach it.
//
// Providers:
//   - ngrok: embedded Go SDK (no external binary). Needs a free authtoken;
//     with the free static domain the URL never changes.
//   - cloudflared: Quick Tunnel, no account. The binary is downloaded on
//     first use from the official GitHub release and verified against the
//     SHA-256 digest GitHub publishes for the asset. URL changes every start.
package tunnel

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/a27259954/adbridge/internal/config"
)

type State string

const (
	StateOff      State = "off"
	StateStarting State = "starting"
	StateOn       State = "on"
	StateError    State = "error"
)

type Status struct {
	Provider     string `json:"provider"`
	State        State  `json:"state"`
	URL          string `json:"url,omitempty"`
	Error        string `json:"error,omitempty"`
	Progress     string `json:"progress,omitempty"`
	StartedAt    int64  `json:"startedAt,omitempty"` // unix ms
	LastActivity int64  `json:"lastActivity,omitempty"`
	IdleMinutes  int    `json:"idleMinutes"`
	StopsAt      int64  `json:"stopsAt,omitempty"` // unix ms, 0 = never
}

// session is a running provider.
type session struct {
	url  string
	stop func()
}

type provider func(ctx context.Context, cfg config.Tunnel, h http.Handler, progress func(string)) (*session, error)

var providers = map[string]provider{
	"ngrok":       startNgrok,
	"cloudflared": startCloudflared,
}

type Manager struct {
	cfg      *config.Store
	handler  http.Handler
	log      *slog.Logger
	onChange func(Status)

	mu     sync.Mutex
	status Status
	sess   *session
	cancel context.CancelFunc
	gen    int
}

func New(cfg *config.Store, h http.Handler, log *slog.Logger, onChange func(Status)) *Manager {
	if onChange == nil {
		onChange = func(Status) {}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{cfg: cfg, handler: h, log: log, onChange: onChange, status: Status{State: StateOff, Provider: cfg.Get().Tunnel.Provider}}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.withDeadline(m.status)
}

func (m *Manager) withDeadline(s Status) Status {
	s.IdleMinutes = m.cfg.Get().Tunnel.IdleMinutes
	s.StopsAt = 0
	if s.State == StateOn && s.IdleMinutes > 0 {
		last := max(s.LastActivity, s.StartedAt)
		s.StopsAt = last + int64(s.IdleMinutes)*int64(time.Minute/time.Millisecond)
	}
	return s
}

func (m *Manager) emit() {
	s := m.withDeadline(m.status)
	go m.onChange(s)
}

// Start brings the tunnel up asynchronously; progress is reported via onChange.
func (m *Manager) Start() error {
	cfg := m.cfg.Get()
	start, ok := providers[cfg.Tunnel.Provider]
	if !ok {
		return errors.New("unknown tunnel provider " + cfg.Tunnel.Provider)
	}
	if cfg.MCP.Token == "" {
		return errors.New("refusing to open a tunnel without an MCP token")
	}
	m.Stop()
	m.mu.Lock()
	m.gen++
	gen := m.gen
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.status = Status{Provider: cfg.Tunnel.Provider, State: StateStarting}
	m.emit()
	m.mu.Unlock()

	go func() {
		progress := func(p string) {
			m.mu.Lock()
			if m.gen == gen && m.status.State == StateStarting {
				m.status.Progress = p
				m.emit()
			}
			m.mu.Unlock()
		}
		sess, err := start(ctx, cfg.Tunnel, m.handler, progress)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.gen != gen { // stopped or restarted meanwhile
			if sess != nil {
				go sess.stop()
			}
			return
		}
		if err != nil {
			m.log.Warn("tunnel failed", "provider", cfg.Tunnel.Provider, "err", err)
			m.status = Status{Provider: cfg.Tunnel.Provider, State: StateError, Error: err.Error()}
			m.emit()
			return
		}
		now := time.Now().UnixMilli()
		m.sess = sess
		m.status = Status{Provider: cfg.Tunnel.Provider, State: StateOn, URL: sess.url, StartedAt: now}
		m.emit()
		go m.idleWatch(ctx, gen)
	}()
	return nil
}

// Stop closes the tunnel.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.gen++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	sess := m.sess
	m.sess = nil
	changed := m.status.State != StateOff
	m.status = Status{Provider: m.cfg.Get().Tunnel.Provider, State: StateOff}
	if changed {
		m.emit()
	}
	m.mu.Unlock()
	if sess != nil {
		sess.stop()
	}
}

// Touch records remote activity (resets the idle timer).
func (m *Manager) Touch() {
	m.mu.Lock()
	if m.status.State == StateOn {
		m.status.LastActivity = time.Now().UnixMilli()
	}
	m.mu.Unlock()
}

func (m *Manager) idleWatch(ctx context.Context, gen int) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st := m.Status()
		if st.StopsAt > 0 && time.Now().UnixMilli() > st.StopsAt {
			m.mu.Lock()
			same := m.gen == gen
			m.mu.Unlock()
			if same {
				m.log.Info("tunnel idle timeout")
				m.Stop()
			}
			return
		}
	}
}
