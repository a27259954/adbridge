package adb

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// Socket specs accepted by forward/reverse and Dial, e.g. tcp:8080,
// localabstract:chrome_devtools_remote, jdwp:1234.
var socketSpecRe = regexp.MustCompile(`^(tcp:\d{1,5}|localabstract:[A-Za-z0-9_.\-@]+|localreserved:[A-Za-z0-9_.\-]+|localfilesystem:[A-Za-z0-9_./\-]+|jdwp:\d+)$`)

// ValidSocketSpec reports whether s is a supported adb socket spec.
func ValidSocketSpec(s string) bool { return socketSpecRe.MatchString(s) }

// Forward is one forward or reverse rule.
type Forward struct {
	Serial string `json:"serial"`
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

func checkSpecs(specs ...string) error {
	for _, s := range specs {
		if !ValidSocketSpec(s) {
			return fmt.Errorf("invalid socket spec %q (use tcp:PORT, localabstract:NAME or jdwp:PID)", s)
		}
	}
	return nil
}

// AddForward maps a PC socket to a device socket (`adb forward`). With
// local "tcp:0" adb picks a free port, which is returned as "tcp:N".
func (c *Client) AddForward(ctx context.Context, serial, local, remote string) (string, error) {
	if err := checkSpecs(local, remote); err != nil {
		return "", err
	}
	out, err := c.run(ctx, "-s", serial, "forward", local, remote)
	if err != nil {
		return "", err
	}
	if local == "tcp:0" && strings.TrimSpace(out) != "" {
		return "tcp:" + strings.TrimSpace(out), nil
	}
	return local, nil
}

// RemoveForward removes a forward rule by its PC-side spec.
func (c *Client) RemoveForward(ctx context.Context, serial, local string) error {
	if err := checkSpecs(local); err != nil {
		return err
	}
	_, err := c.run(ctx, "-s", serial, "forward", "--remove", local)
	return err
}

// ListForwards lists forward rules for serial.
func (c *Client) ListForwards(ctx context.Context, serial string) ([]Forward, error) {
	out, err := c.run(ctx, "forward", "--list")
	if err != nil {
		return nil, err
	}
	var res []Forward
	for _, f := range parseForwards(out) {
		if f.Serial == serial {
			res = append(res, f)
		}
	}
	return res, nil
}

// AddReverse lets the device reach a PC socket (`adb reverse`): device-side
// remote → PC-side local.
func (c *Client) AddReverse(ctx context.Context, serial, remote, local string) error {
	if err := checkSpecs(remote, local); err != nil {
		return err
	}
	_, err := c.run(ctx, "-s", serial, "reverse", remote, local)
	return err
}

// RemoveReverse removes a reverse rule by its device-side spec.
func (c *Client) RemoveReverse(ctx context.Context, serial, remote string) error {
	if err := checkSpecs(remote); err != nil {
		return err
	}
	_, err := c.run(ctx, "-s", serial, "reverse", "--remove", remote)
	return err
}

// ListReverses lists reverse rules for serial.
func (c *Client) ListReverses(ctx context.Context, serial string) ([]Forward, error) {
	out, err := c.run(ctx, "-s", serial, "reverse", "--list")
	if err != nil {
		return nil, err
	}
	res := parseForwards(out)
	for i := range res {
		res[i].Serial = serial
	}
	return res, nil
}

// parseForwards parses "serial local remote" lines (reverse --list prints
// a transport name in the first column).
func parseForwards(out string) []Forward {
	var res []Forward
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 {
			res = append(res, Forward{Serial: f[0], Local: f[1], Remote: f[2]})
		}
	}
	return res
}

// Dial opens a stream to a socket on the device (e.g. "tcp:8080") through
// the adb transport, without creating a forward rule or a PC port.
func (c *Client) Dial(ctx context.Context, serial, spec string) (net.Conn, error) {
	if err := checkSpecs(spec); err != nil {
		return nil, err
	}
	return c.openService(ctx, serial, spec)
}
