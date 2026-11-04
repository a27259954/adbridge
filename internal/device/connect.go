package device

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/a27259954/adbridge/internal/adb"
)

// NormalizeAddr adds the classic adb port when addr has none.
func NormalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return net.JoinHostPort(addr, "5555")
	}
	return addr
}

// Connect connects to ip[:port] and re-enables auto reconnect for it.
func (m *Manager) Connect(ctx context.Context, addr string) error {
	addr = NormalizeAddr(addr)
	if err := m.ADB.Connect(ctx, addr); err != nil {
		return err
	}
	// The identity is learned asynchronously; re-enable reconnect for any
	// remembered device that used this address.
	for _, k := range m.known.all() {
		if k.LastAddr == addr && !k.AutoReconnect {
			m.known.update(k.ID, func(k *Known) { k.AutoReconnect = true })
		}
	}
	return nil
}

// Pair pairs with a device showing a 6-digit code (Android 11+), then
// waits briefly for its connect service and connects.
func (m *Manager) Pair(ctx context.Context, addr, code string) error {
	if err := m.ADB.Pair(ctx, strings.TrimSpace(addr), strings.TrimSpace(code)); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(strings.TrimSpace(addr))
	return m.ConnectAfterPair(ctx, host)
}

// ConnectAfterPair polls mDNS for host's _adb-tls-connect service and
// connects to it. The adb server often auto-connects on its own, in which
// case this returns as soon as the device is online.
func (m *Manager) ConnectAfterPair(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		for _, d := range m.List() {
			for _, t := range d.Transports {
				if t.State == "device" && t.Kind == "wifi" && strings.HasPrefix(t.Serial, host+":") {
					return nil
				}
			}
		}
		if svcs, err := m.ADB.MDNSServices(ctx); err == nil {
			for _, s := range svcs {
				if s.Type == adb.MDNSConnect && strings.HasPrefix(s.Addr, host+":") {
					if err := m.ADB.Connect(ctx, s.Addr); err == nil {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("paired, but the device's wireless debugging port was not found; enter ip:port from the phone's Wireless debugging screen")
		case <-time.After(time.Second):
		}
	}
}

// Disconnect drops a device's Wi-Fi transports and stops auto reconnect.
func (m *Manager) Disconnect(ctx context.Context, id string) error {
	d, err := m.Resolve(id)
	if err != nil {
		return err
	}
	m.known.update(d.ID, func(k *Known) { k.AutoReconnect = false })
	var errs []error
	for _, t := range d.Transports {
		if t.Kind == "wifi" {
			errs = append(errs, m.ADB.Disconnect(ctx, t.Serial))
		}
	}
	m.recompute()
	return errors.Join(errs...)
}

// Forget disconnects and removes a device from the remembered list.
func (m *Manager) Forget(ctx context.Context, id string) error {
	d, err := m.Resolve(id)
	if err != nil {
		return err
	}
	_ = m.Disconnect(ctx, d.ID)
	m.known.remove(d.ID)
	m.mu.Lock()
	delete(m.rings, d.ID)
	m.mu.Unlock()
	m.recompute()
	return nil
}

var inetRe = regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)/`)

// USBToWifi switches a USB-connected device to TCP mode on port 5555 and
// connects over Wi-Fi. Works on every Android version.
func (m *Manager) USBToWifi(ctx context.Context, id string) (string, error) {
	d, err := m.Resolve(id)
	if err != nil {
		return "", err
	}
	var usb string
	for _, t := range d.Transports {
		if t.Kind == "usb" && t.State == "device" {
			usb = t.Serial
		}
	}
	if usb == "" {
		return "", fmt.Errorf("%s has no online USB connection", d.Name)
	}
	res, err := m.ADB.Shell(ctx, usb, "ip -f inet addr show wlan0")
	if err != nil {
		return "", err
	}
	mt := inetRe.FindStringSubmatch(string(res.Stdout))
	if mt == nil {
		return "", errors.New("could not read the phone's Wi-Fi IP (is Wi-Fi on?)")
	}
	addr := mt[1] + ":5555"
	if err := m.ADB.TCPIP(ctx, usb, 5555); err != nil {
		return "", err
	}
	var last error
	for i := 0; i < 8; i++ {
		sleep(ctx, 1500*time.Millisecond)
		if last = m.Connect(ctx, addr); last == nil {
			return addr, nil
		}
	}
	return "", fmt.Errorf("tcpip enabled but connect to %s failed: %w", addr, last)
}

func (m *Manager) SetAlias(id, alias string) error {
	return m.updateKnown(id, func(k *Known) { k.Alias = strings.TrimSpace(alias) })
}

func (m *Manager) SetAIEnabled(id string, on bool) error {
	return m.updateKnown(id, func(k *Known) { k.AIEnabled = on })
}

func (m *Manager) SetAutoReconnect(id string, on bool) error {
	return m.updateKnown(id, func(k *Known) { k.AutoReconnect = on })
}

func (m *Manager) updateKnown(id string, fn func(*Known)) error {
	d, err := m.Resolve(id)
	if err != nil {
		return err
	}
	m.known.update(d.ID, fn)
	m.recompute()
	return nil
}

// reconnectLoop brings remembered wireless devices back. Wireless debugging
// picks a new port after every reboot/toggle, so the device is located by
// its mDNS instance name ("adb-<serialno>-xxxxxx") before falling back to
// the last known address.
func (m *Manager) reconnectLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if m.TrackError() != nil {
			continue
		}
		online := map[string]bool{}
		for _, d := range m.List() {
			if d.State == StateOnline {
				online[d.ID] = true
			}
		}
		var due []Known
		now := time.Now()
		m.mu.Lock()
		for _, k := range m.known.all() {
			if !k.Wireless || !k.AutoReconnect || online[k.ID] {
				delete(m.retry, k.ID)
				continue
			}
			if r := m.retry[k.ID]; now.Before(r.next) {
				continue
			}
			due = append(due, k)
		}
		m.mu.Unlock()
		if len(due) == 0 {
			continue
		}
		svcs, _ := m.ADB.MDNSServices(ctx)
		for _, k := range due {
			addr := k.LastAddr
			for _, s := range svcs {
				if s.Type == adb.MDNSConnect && strings.HasPrefix(s.Name, "adb-"+k.ID+"-") {
					addr = s.Addr
				}
			}
			if addr == "" {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			err := m.ADB.Connect(cctx, addr)
			cancel()
			m.mu.Lock()
			r := m.retry[k.ID]
			if err != nil {
				r.delay = min(max(r.delay*2, 5*time.Second), time.Minute)
				r.next = time.Now().Add(r.delay)
				m.retry[k.ID] = r
			} else {
				delete(m.retry, k.ID)
			}
			m.mu.Unlock()
			if err == nil {
				m.log.Info("reconnected", "device", k.ID, "addr", addr)
			}
		}
	}
}

// batteryLoop refreshes battery levels once a minute.
func (m *Manager) batteryLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		changed := false
		for _, d := range m.List() {
			if d.State != StateOnline {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			res, err := m.ADB.Shell(cctx, d.Active, "dumpsys battery | grep -m1 level")
			cancel()
			if err != nil {
				continue
			}
			lvl := parseBattery(string(res.Stdout))
			m.mu.Lock()
			if inf := m.info[d.Active]; inf != nil && inf.battery != lvl {
				inf.battery = lvl
				changed = true
			}
			m.mu.Unlock()
		}
		if changed {
			m.recompute()
		}
	}
}
