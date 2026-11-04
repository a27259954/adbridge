package adb

import "strings"

// Entry is one line of `adb devices -l`.
type Entry struct {
	Serial      string `json:"serial"`
	State       string `json:"state"` // device, offline, unauthorized, authorizing, ...
	Product     string `json:"product,omitempty"`
	Model       string `json:"model,omitempty"`
	Device      string `json:"device,omitempty"`
	USB         string `json:"usb,omitempty"`
	TransportID string `json:"transportId,omitempty"`
}

// Online reports whether the transport is usable.
func (e Entry) Online() bool { return e.State == "device" }

// Wireless reports whether the transport is TCP (ip:port or mDNS name).
func (e Entry) Wireless() bool { return IsWirelessSerial(e.Serial) }

// IsWirelessSerial reports whether serial names a TCP transport.
func IsWirelessSerial(serial string) bool {
	if strings.Contains(serial, "._adb-tls-connect._tcp") {
		return true
	}
	if strings.HasPrefix(serial, "emulator-") {
		return false
	}
	i := strings.LastIndexByte(serial, ':')
	return i > 0 && i < len(serial)-1
}

// ParseDevices parses the payload of host:devices-l / host:track-devices-l.
func ParseDevices(s string) []Entry {
	var out []Entry
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		e := Entry{Serial: f[0], State: f[1]}
		// "no permissions (...)" and similar multi-word states.
		rest := f[2:]
		for i, kv := range rest {
			k, v, ok := strings.Cut(kv, ":")
			if !ok {
				if i == 0 && e.State != "device" {
					e.State += " " + kv
				}
				continue
			}
			switch k {
			case "product":
				e.Product = v
			case "model":
				e.Model = v
			case "device":
				e.Device = v
			case "usb":
				e.USB = v
			case "transport_id":
				e.TransportID = v
			}
		}
		out = append(out, e)
	}
	return out
}
