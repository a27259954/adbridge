// Package device turns raw adb transports into stable, user-facing devices.
//
// One physical phone can appear as several transports (USB serial, ip:port,
// mDNS name). Manager groups them by ro.serialno, picks the transport to use
// (Wi-Fi first, USB as fallback), remembers wireless devices across runs,
// reconnects them when their port changes, and keeps a logcat ring buffer
// per device so queries never have to wait on the device.
package device

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a27259954/adbridge/internal/adb"
)

// Device states.
const (
	StateOnline       = "online"
	StateOffline      = "offline"
	StateUnauthorized = "unauthorized"
	StateDisconnected = "disconnected" // remembered, no transport right now
)

// Transport is one adb connection to a device.
type Transport struct {
	Serial string `json:"serial"`
	Kind   string `json:"kind"` // usb | wifi
	State  string `json:"state"`
}

// Device is the merged, user-facing view of a phone.
type Device struct {
	ID            string      `json:"id"`
	Alias         string      `json:"alias"`
	Name          string      `json:"name"` // alias or model
	Model         string      `json:"model"`
	Manufacturer  string      `json:"manufacturer"`
	Android       string      `json:"android"`
	SDK           int         `json:"sdk"`
	State         string      `json:"state"`
	Active        string      `json:"active"` // serial used for commands
	ActiveKind    string      `json:"activeKind"`
	Transports    []Transport `json:"transports"`
	Battery       int         `json:"battery"` // -1 unknown
	AIEnabled     bool        `json:"aiEnabled"`
	Wireless      bool        `json:"wireless"`
	AutoReconnect bool        `json:"autoReconnect"`
	LastAddr      string      `json:"lastAddr"`

	identified bool // props fetched for Active
}

type serialInfo struct {
	id           string
	model        string
	manufacturer string
	android      string
	sdk          int
	battery      int
	batteryAt    time.Time
}

// Manager owns device state. Create with New, then call Run.
type Manager struct {
	ADB *adb.Client
	log *slog.Logger

	known *knownStore

	mu       sync.Mutex
	entries  []adb.Entry
	info     map[string]*serialInfo // by serial
	fetching map[string]bool
	devices  []Device
	rings    map[string]*Ring
	logcat   map[string]*logRunner // by device id
	retry    map[string]retryState
	trees    map[string][]UINode
	subs     map[int]func([]Device)
	nextSub  int
	trackErr error
}

type logRunner struct {
	serial string
	cancel context.CancelFunc
}

type retryState struct {
	next  time.Time
	delay time.Duration
}

// New creates a Manager. knownPath persists remembered devices ("" = memory only).
func New(client *adb.Client, knownPath string, log *slog.Logger) (*Manager, error) {
	ks, err := openKnown(knownPath)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{
		ADB:      client,
		log:      log,
		known:    ks,
		info:     map[string]*serialInfo{},
		fetching: map[string]bool{},
		rings:    map[string]*Ring{},
		logcat:   map[string]*logRunner{},
		retry:    map[string]retryState{},
		trees:    map[string][]UINode{},
		subs:     map[int]func([]Device){},
	}
	m.recompute()
	return m, nil
}

// Subscribe calls fn with the device list after every change. It returns
// an unsubscribe func.
func (m *Manager) Subscribe(fn func([]Device)) func() {
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = fn
	m.mu.Unlock()
	return func() { m.mu.Lock(); delete(m.subs, id); m.mu.Unlock() }
}

// List returns a snapshot of all devices.
func (m *Manager) List() []Device {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Device(nil), m.devices...)
}

// TrackError is the last error from the adb server connection, if any.
func (m *Manager) TrackError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.trackErr
}

// Run tracks devices until ctx is cancelled, restarting the adb server
// connection whenever it drops.
func (m *Manager) Run(ctx context.Context) {
	go m.reconnectLoop(ctx)
	go m.batteryLoop(ctx)
	for ctx.Err() == nil {
		err := m.ADB.EnsureServer(ctx)
		var ch <-chan []adb.Entry
		if err == nil {
			ch, err = m.ADB.TrackDevices(ctx)
		}
		m.mu.Lock()
		m.trackErr = err
		m.mu.Unlock()
		if err != nil {
			m.log.Warn("adb server unavailable", "err", err)
			m.setEntries(nil)
			sleep(ctx, 3*time.Second)
			continue
		}
		for list := range ch {
			m.setEntries(list)
		}
		m.setEntries(nil)
		sleep(ctx, time.Second)
	}
	m.stopAllLogcat()
}

func (m *Manager) setEntries(list []adb.Entry) {
	m.mu.Lock()
	m.entries = list
	var toFetch []string
	present := map[string]bool{}
	for _, e := range list {
		present[e.Serial] = true
		if e.Online() && m.info[e.Serial] == nil && !m.fetching[e.Serial] {
			m.fetching[e.Serial] = true
			toFetch = append(toFetch, e.Serial)
		}
	}
	// Forget per-serial info for vanished or no longer usable transports so
	// a re-plugged device is re-identified.
	for s := range m.info {
		online := false
		for _, e := range list {
			if e.Serial == s && e.Online() {
				online = true
			}
		}
		if !present[s] || !online {
			delete(m.info, s)
		}
	}
	m.mu.Unlock()
	for _, s := range toFetch {
		go m.fetchInfo(s)
	}
	m.recompute()
}

func (m *Manager) fetchInfo(serial string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() { m.mu.Lock(); delete(m.fetching, serial); m.mu.Unlock() }()

	res, err := m.ADB.Shell(ctx, serial, "getprop; echo __BATT__; dumpsys battery | grep -m1 level")
	if err != nil {
		m.log.Warn("getprop failed", "serial", serial, "err", err)
		return
	}
	out := string(res.Stdout)
	props, batt, _ := strings.Cut(out, "__BATT__")
	p := ParseProps(props)
	info := &serialInfo{
		id:           DeviceID(p, serial),
		model:        p["ro.product.model"],
		manufacturer: p["ro.product.manufacturer"],
		android:      p["ro.build.version.release"],
		battery:      parseBattery(batt),
		batteryAt:    time.Now(),
	}
	info.sdk, _ = strconv.Atoi(p["ro.build.version.sdk"])

	wireless := adb.IsWirelessSerial(serial)
	m.known.update(info.id, func(k *Known) {
		k.Model = info.model
		k.LastSeen = time.Now()
		if wireless {
			if !k.Wireless {
				k.AutoReconnect = true
			}
			k.Wireless = true
			if _, _, err := net.SplitHostPort(serial); err == nil && !strings.Contains(serial, "._adb-tls-connect") {
				k.LastAddr = serial
			}
		}
	})
	m.mu.Lock()
	stillThere := false
	for _, e := range m.entries {
		if e.Serial == serial && e.Online() {
			stillThere = true
		}
	}
	if stillThere {
		m.info[serial] = info
	}
	m.mu.Unlock()
	m.recompute()
}

// recompute rebuilds the merged device list and reconciles logcat runners.
func (m *Manager) recompute() {
	m.mu.Lock()
	byID := map[string]*Device{}
	var order []string
	get := func(id string) *Device {
		d, ok := byID[id]
		if !ok {
			d = &Device{ID: id, Battery: -1, State: StateDisconnected, AIEnabled: true}
			byID[id] = d
			order = append(order, id)
		}
		return d
	}
	for _, e := range m.entries {
		id := e.Serial
		inf := m.info[e.Serial]
		if inf != nil {
			id = inf.id
		}
		d := get(id)
		kind := "usb"
		if e.Wireless() {
			kind = "wifi"
		}
		d.Transports = append(d.Transports, Transport{Serial: e.Serial, Kind: kind, State: e.State})
		if inf != nil {
			d.Model, d.Manufacturer, d.Android, d.SDK, d.Battery = inf.model, inf.manufacturer, inf.android, inf.sdk, inf.battery
		} else if d.Model == "" {
			d.Model = strings.ReplaceAll(e.Model, "_", " ")
		}
	}
	for _, k := range m.known.all() {
		if _, ok := byID[k.ID]; !ok && !k.Wireless {
			continue // USB-only devices disappear when unplugged
		}
		d := get(k.ID)
		if d.Model == "" {
			d.Model = k.Model
		}
	}
	for _, d := range byID {
		if k, ok := m.known.get(d.ID); ok {
			d.Alias, d.AIEnabled, d.Wireless, d.AutoReconnect, d.LastAddr = k.Alias, k.AIEnabled, k.Wireless, k.AutoReconnect, k.LastAddr
		}
		d.Name = d.Alias
		if d.Name == "" {
			d.Name = d.Model
		}
		if d.Name == "" {
			d.Name = d.ID
		}
		pickActive(d)
		d.identified = d.Active != "" && m.info[d.Active] != nil
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := byID[order[i]], byID[order[j]]
		if (a.State == StateOnline) != (b.State == StateOnline) {
			return a.State == StateOnline
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	devices := make([]Device, 0, len(order))
	for _, id := range order {
		devices = append(devices, *byID[id])
	}
	m.devices = devices
	subs := make([]func([]Device), 0, len(m.subs))
	for _, f := range m.subs {
		subs = append(subs, f)
	}
	m.mu.Unlock()

	m.reconcileLogcat(devices)
	for _, f := range subs {
		f(append([]Device(nil), devices...))
	}
}

// pickActive prefers an online Wi-Fi transport, then online USB.
func pickActive(d *Device) {
	rank := func(t Transport) int {
		switch {
		case t.State == "device" && t.Kind == "wifi":
			return 4
		case t.State == "device":
			return 3
		case t.State == "unauthorized":
			return 2
		default:
			return 1
		}
	}
	sort.SliceStable(d.Transports, func(i, j int) bool { return rank(d.Transports[i]) > rank(d.Transports[j]) })
	if len(d.Transports) == 0 {
		d.State, d.Active, d.ActiveKind = StateDisconnected, "", ""
		return
	}
	t := d.Transports[0]
	switch t.State {
	case "device":
		d.State, d.Active, d.ActiveKind = StateOnline, t.Serial, t.Kind
	case "unauthorized":
		d.State = StateUnauthorized
	default:
		d.State = StateOffline
	}
}

// ---- lookup ---------------------------------------------------------------

// ErrNoDevice is returned when a query matches nothing usable.
var ErrNoDevice = errors.New("no matching device")

// Resolve finds a device by id, alias, name or transport serial. An empty
// query selects the only online device.
func (m *Manager) Resolve(query string) (Device, error) {
	devs := m.List()
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		var online []Device
		for _, d := range devs {
			if d.State == StateOnline {
				online = append(online, d)
			}
		}
		switch len(online) {
		case 1:
			return online[0], nil
		case 0:
			return Device{}, fmt.Errorf("%w: no device is online", ErrNoDevice)
		default:
			return Device{}, fmt.Errorf("%d devices online; specify one by id or alias", len(online))
		}
	}
	for _, d := range devs {
		if strings.ToLower(d.ID) == q || strings.ToLower(d.Alias) == q || strings.ToLower(d.Name) == q {
			return d, nil
		}
		for _, t := range d.Transports {
			if strings.ToLower(t.Serial) == q {
				return d, nil
			}
		}
	}
	return Device{}, fmt.Errorf("%w: %q", ErrNoDevice, query)
}

// Online resolves query and requires the device to be usable.
func (m *Manager) Online(query string) (Device, error) {
	d, err := m.Resolve(query)
	if err != nil {
		return d, err
	}
	if d.State != StateOnline {
		return d, fmt.Errorf("device %s is %s", d.Name, d.State)
	}
	return d, nil
}

// ---- helpers -----------------------------------------------------------------

// ParseProps parses `getprop` output ("[key]: [value]").
func ParseProps(s string) map[string]string {
	p := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "]: [")
		if !ok || !strings.HasPrefix(k, "[") || !strings.HasSuffix(v, "]") {
			continue
		}
		p[k[1:]] = v[:len(v)-1]
	}
	return p
}

// DeviceID returns a stable identity for a phone across transports.
func DeviceID(props map[string]string, serial string) string {
	for _, k := range []string{"ro.serialno", "ro.boot.serialno"} {
		v := props[k]
		if v != "" && v != "unknown" && v != "0123456789ABCDEF" {
			return v
		}
	}
	return serial
}

func parseBattery(s string) int {
	_, v, ok := strings.Cut(s, "level:")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return -1
	}
	return n
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
