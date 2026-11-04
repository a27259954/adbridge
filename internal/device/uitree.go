package device

import (
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// UINode is a compact, numbered view of an on-screen element, designed so
// an AI can say "tap element 12" instead of guessing coordinates.
type UINode struct {
	Index     int    `json:"index"`
	Class     string `json:"class"`
	Text      string `json:"text,omitempty"`
	Desc      string `json:"desc,omitempty"`
	ID        string `json:"id,omitempty"`
	Package   string `json:"package,omitempty"`
	X         int    `json:"x"` // center
	Y         int    `json:"y"`
	Bounds    string `json:"bounds"`
	Clickable bool   `json:"clickable,omitempty"`
	Scroll    bool   `json:"scrollable,omitempty"`
	Checked   bool   `json:"checked,omitempty"`
	Focused   bool   `json:"focused,omitempty"`
	Enabled   bool   `json:"enabled"`
}

type xmlNode struct {
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []xmlNode  `xml:"node"`
}

var boundsRe = regexp.MustCompile(`^\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]$`)

const dumpPath = "/data/local/tmp/adbridge-ui.xml"

// UITree dumps the current UI hierarchy via uiautomator and caches it so
// TapElement can resolve indices.
func (m *Manager) UITree(ctx context.Context, id string) ([]UINode, error) {
	d, err := m.Online(id)
	if err != nil {
		return nil, err
	}
	out, err := m.sh(ctx, d.Active, "uiautomator dump "+dumpPath+" >/dev/null && cat "+dumpPath+"; rm -f "+dumpPath)
	if err != nil {
		return nil, fmt.Errorf("uiautomator dump: %w", err)
	}
	nodes, err := ParseUITree(out)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.trees[d.ID] = nodes
	m.mu.Unlock()
	return nodes, nil
}

// TapElement taps the center of element idx from the last UITree call.
func (m *Manager) TapElement(ctx context.Context, id string, idx int) (UINode, error) {
	d, err := m.Online(id)
	if err != nil {
		return UINode{}, err
	}
	m.mu.Lock()
	tree := m.trees[d.ID]
	m.mu.Unlock()
	if tree == nil {
		return UINode{}, fmt.Errorf("no UI tree cached for %s; call ui_tree first", d.Name)
	}
	for _, n := range tree {
		if n.Index == idx {
			return n, m.Tap(ctx, d.ID, n.X, n.Y)
		}
	}
	return UINode{}, fmt.Errorf("element %d not in the last UI tree (%d elements)", idx, len(tree))
}

// ParseUITree flattens a uiautomator XML dump, keeping only nodes that carry
// meaning (text, description, resource id) or can be interacted with.
func ParseUITree(dump string) ([]UINode, error) {
	start := strings.Index(dump, "<hierarchy")
	if start < 0 {
		return nil, fmt.Errorf("no UI hierarchy in dump output: %.200s", dump)
	}
	end := strings.LastIndex(dump, "</hierarchy>")
	if end < 0 {
		return nil, fmt.Errorf("truncated UI hierarchy")
	}
	var root xmlNode
	if err := xml.Unmarshal([]byte(dump[start:end+len("</hierarchy>")]), &root); err != nil {
		return nil, err
	}
	var out []UINode
	var walk func(n xmlNode)
	walk = func(n xmlNode) {
		a := map[string]string{}
		for _, at := range n.Attrs {
			a[at.Name.Local] = at.Value
		}
		node := UINode{
			Class:     shortClass(a["class"]),
			Text:      a["text"],
			Desc:      a["content-desc"],
			ID:        a["resource-id"],
			Package:   a["package"],
			Bounds:    a["bounds"],
			Clickable: a["clickable"] == "true" || a["long-clickable"] == "true",
			Scroll:    a["scrollable"] == "true",
			Checked:   a["checked"] == "true",
			Focused:   a["focused"] == "true",
			Enabled:   a["enabled"] != "false",
		}
		if mt := boundsRe.FindStringSubmatch(node.Bounds); mt != nil {
			x1, _ := strconv.Atoi(mt[1])
			y1, _ := strconv.Atoi(mt[2])
			x2, _ := strconv.Atoi(mt[3])
			y2, _ := strconv.Atoi(mt[4])
			node.X, node.Y = (x1+x2)/2, (y1+y2)/2
			if x2 <= x1 || y2 <= y1 {
				node.Bounds = "" // zero-size: not useful
			}
		}
		meaningful := node.Text != "" || node.Desc != "" || node.Clickable || node.Scroll || a["checkable"] == "true"
		if a["class"] != "" && node.Bounds != "" && meaningful {
			node.Index = len(out)
			out = append(out, node)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, c := range root.Children {
		walk(c)
	}
	return out, nil
}

func shortClass(c string) string {
	if i := strings.LastIndexByte(c, '.'); i >= 0 {
		return c[i+1:]
	}
	return c
}

// FormatUITree renders nodes one per line, e.g.
// [12] Button "Log in" id=login (540,1820) click
func FormatUITree(nodes []UINode) string {
	var b strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&b, "[%d] %s", n.Index, n.Class)
		if n.Text != "" {
			fmt.Fprintf(&b, " %q", n.Text)
		}
		if n.Desc != "" {
			fmt.Fprintf(&b, " desc=%q", n.Desc)
		}
		if n.ID != "" {
			id := n.ID
			if i := strings.Index(id, ":id/"); i >= 0 {
				id = id[i+4:]
			}
			fmt.Fprintf(&b, " id=%s", id)
		}
		fmt.Fprintf(&b, " (%d,%d)", n.X, n.Y)
		var flags []string
		if n.Clickable {
			flags = append(flags, "click")
		}
		if n.Scroll {
			flags = append(flags, "scroll")
		}
		if n.Checked {
			flags = append(flags, "checked")
		}
		if n.Focused {
			flags = append(flags, "focused")
		}
		if !n.Enabled {
			flags = append(flags, "disabled")
		}
		if len(flags) > 0 {
			b.WriteString(" " + strings.Join(flags, ","))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
