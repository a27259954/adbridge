// Package pairing implements Android 11+ "Pair device with QR code".
//
// The host shows a QR encoding WIFI:T:ADB;S:<name>;P:<password>;; — the
// phone scans it and advertises _adb-tls-pairing._tcp under <name>. We find
// that service through the adb server's mDNS backend, run `adb pair`, then
// connect to the phone's _adb-tls-connect._tcp service.
package pairing

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/a27259954/adbridge/internal/adb"
	"github.com/a27259954/adbridge/internal/device"
)

type State string

const (
	StateIdle       State = "idle"
	StateWaiting    State = "waiting"    // QR shown, waiting for the phone
	StatePairing    State = "pairing"    // adb pair running
	StateConnecting State = "connecting" // paired, connecting
	StateDone       State = "done"
	StateError      State = "error"
)

// Status is what the UI renders.
type Status struct {
	State   State  `json:"state"`
	Name    string `json:"name"`
	QR      string `json:"qr"` // data:image/png;base64,...
	Payload string `json:"payload"`
	Addr    string `json:"addr,omitempty"`
	Error   string `json:"error,omitempty"`
	Expires int64  `json:"expires"` // unix ms
}

// Timeout is how long a QR session stays valid.
const Timeout = 2 * time.Minute

type Service struct {
	mgr      *device.Manager
	onChange func(Status)

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc
}

func New(mgr *device.Manager, onChange func(Status)) *Service {
	if onChange == nil {
		onChange = func(Status) {}
	}
	return &Service{mgr: mgr, onChange: onChange, status: Status{State: StateIdle}}
}

func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Start begins a new QR session, cancelling any previous one.
func (s *Service) Start() (Status, error) {
	name := "adbridge-" + randString(6)
	pass := randString(10)
	payload := "WIFI:T:ADB;S:" + name + ";P:" + pass + ";;"
	png, err := qrcode.Encode(payload, qrcode.Medium, 320)
	if err != nil {
		return Status{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = cancel
	s.status = Status{
		State:   StateWaiting,
		Name:    name,
		Payload: payload,
		QR:      "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		Expires: time.Now().Add(Timeout).UnixMilli(),
	}
	st := s.status
	s.mu.Unlock()
	s.onChange(st)
	go s.run(ctx, name, pass)
	return st, nil
}

// Cancel stops the current session.
func (s *Service) Cancel() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.status = Status{State: StateIdle}
	st := s.status
	s.mu.Unlock()
	s.onChange(st)
}

func (s *Service) set(name string, fn func(*Status)) {
	s.mu.Lock()
	if s.status.Name != name { // superseded session
		s.mu.Unlock()
		return
	}
	fn(&s.status)
	st := s.status
	s.mu.Unlock()
	s.onChange(st)
}

func (s *Service) run(ctx context.Context, name, pass string) {
	fail := func(err error) {
		if errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("timed out waiting for the phone; make sure it is on the same Wi-Fi and scanned the code from Developer options → Wireless debugging → Pair device with QR code")
		}
		s.set(name, func(st *Status) { st.State, st.Error = StateError, err.Error() })
	}
	var addr string
	for addr == "" {
		svcs, err := s.mgr.ADB.MDNSServices(ctx)
		if err == nil {
			for _, svc := range svcs {
				if svc.Type == adb.MDNSPairing && svc.Name == name {
					addr = svc.Addr
				}
			}
		}
		if addr != "" {
			break
		}
		select {
		case <-ctx.Done():
			fail(ctx.Err())
			return
		case <-time.After(time.Second):
		}
	}
	s.set(name, func(st *Status) { st.State, st.Addr = StatePairing, addr })
	if err := s.mgr.ADB.Pair(ctx, addr, pass); err != nil {
		fail(err)
		return
	}
	s.set(name, func(st *Status) { st.State = StateConnecting })
	host, _, _ := net.SplitHostPort(addr)
	if err := s.mgr.ConnectAfterPair(context.Background(), host); err != nil {
		fail(err)
		return
	}
	s.set(name, func(st *Status) { st.State = StateDone })
}

const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"

func randString(n int) string {
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b)
}
