package device

import (
	"bufio"
	"context"
	"time"
)

// RingSize is the number of log lines kept per device.
const RingSize = 20_000

// Logcat returns the ring buffer for a device (nil if it was never online).
func (m *Manager) Logcat(id string) *Ring {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rings[id]
}

// ClearLogcat clears the device-side buffer and the local ring.
func (m *Manager) ClearLogcat(ctx context.Context, id string) error {
	d, err := m.Online(id)
	if err != nil {
		return err
	}
	if _, err := m.ADB.Shell(ctx, d.Active, "logcat -c"); err != nil {
		return err
	}
	if r := m.Logcat(d.ID); r != nil {
		r.Clear()
	}
	return nil
}

// reconcileLogcat runs exactly one logcat stream per online device, on its
// active transport.
func (m *Manager) reconcileLogcat(devices []Device) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]string{}
	for _, d := range devices {
		if d.State == StateOnline && d.identified {
			want[d.ID] = d.Active
		}
	}
	for id, r := range m.logcat {
		if want[id] != r.serial {
			r.cancel()
			delete(m.logcat, id)
		}
	}
	for id, serial := range want {
		if _, ok := m.logcat[id]; ok {
			continue
		}
		ring := m.rings[id]
		if ring == nil {
			ring = NewRing(RingSize)
			m.rings[id] = ring
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.logcat[id] = &logRunner{serial: serial, cancel: cancel}
		go m.streamLogcat(ctx, serial, ring)
	}
}

func (m *Manager) streamLogcat(ctx context.Context, serial string, ring *Ring) {
	for ctx.Err() == nil {
		cmd := "logcat -v threadtime -T 500"
		if t := ring.LastTime(); t != "" {
			cmd = "logcat -v threadtime -T '" + t + "'"
		}
		rc, err := m.ADB.ExecStream(ctx, serial, cmd)
		if err != nil {
			sleep(ctx, 2*time.Second)
			continue
		}
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		last := ring.LastTime()
		skipping := last != ""
		for sc.Scan() {
			e, ok := ParseLogLine(sc.Text())
			if !ok {
				continue
			}
			// -T <time> is inclusive; drop lines we already hold.
			if skipping {
				if e.Time <= last {
					continue
				}
				skipping = false
			}
			ring.Add(e)
		}
		rc.Close()
		sleep(ctx, time.Second)
	}
}

func (m *Manager) stopAllLogcat() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.logcat {
		r.cancel()
		delete(m.logcat, id)
	}
}
