package device

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LogEntry is one parsed `logcat -v threadtime` line.
type LogEntry struct {
	Seq   uint64    `json:"seq"`
	At    time.Time `json:"at"`   // host receive time
	Time  string    `json:"time"` // device timestamp "MM-DD hh:mm:ss.mmm"
	PID   int       `json:"pid"`
	TID   int       `json:"tid"`
	Level string    `json:"level"` // V D I W E F
	Tag   string    `json:"tag"`
	Msg   string    `json:"msg"`
}

var threadtimeRe = regexp.MustCompile(`^(\d\d-\d\d \d\d:\d\d:\d\d\.\d{3})\s+(\d+)\s+(\d+)\s+([VDIWEFS])\s+(.*?)\s*: (.*)$`)

// ParseLogLine parses a threadtime line. ok is false for banner lines
// such as "--------- beginning of main".
func ParseLogLine(line string) (e LogEntry, ok bool) {
	m := threadtimeRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if m == nil {
		return e, false
	}
	e.Time = m[1]
	e.PID, _ = strconv.Atoi(m[2])
	e.TID, _ = strconv.Atoi(m[3])
	e.Level, e.Tag, e.Msg = m[4], m[5], m[6]
	return e, true
}

var levelRank = map[string]int{"V": 1, "D": 2, "I": 3, "W": 4, "E": 5, "F": 6, "S": 7}

// LogFilter narrows a ring query. Zero values match everything.
type LogFilter struct {
	MinLevel string         // V/D/I/W/E/F
	Tag      string         // case-insensitive substring
	PIDs     []int          // any of these pids
	Grep     *regexp.Regexp // matched against tag + message
	Since    time.Time      // host time
	Limit    int            // newest N matches (default 200)
}

// Ring is a bounded, concurrency-safe log buffer.
type Ring struct {
	mu       sync.RWMutex
	buf      []LogEntry
	start    int
	n        int
	seq      uint64
	lastTime string
}

func NewRing(size int) *Ring { return &Ring{buf: make([]LogEntry, size)} }

func (r *Ring) Add(e LogEntry) {
	r.mu.Lock()
	r.seq++
	e.Seq = r.seq
	if e.At.IsZero() {
		e.At = time.Now()
	}
	idx := (r.start + r.n) % len(r.buf)
	if r.n == len(r.buf) {
		r.start = (r.start + 1) % len(r.buf)
	} else {
		r.n++
	}
	r.buf[idx] = e
	r.lastTime = e.Time
	r.mu.Unlock()
}

func (r *Ring) at(i int) LogEntry { return r.buf[(r.start+i)%len(r.buf)] }

// LastTime is the device timestamp of the newest entry, used to resume
// `logcat -T` without gaps after a reconnect.
func (r *Ring) LastTime() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastTime
}

// Since returns up to max entries with Seq > seq, oldest first.
func (r *Ring) Since(seq uint64, max int) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	first := 0
	if r.n > 0 {
		oldest := r.at(0).Seq
		if seq >= oldest {
			first = int(seq - oldest + 1)
		}
	}
	if first >= r.n {
		return nil
	}
	if max > 0 && r.n-first > max {
		first = r.n - max
	}
	out := make([]LogEntry, 0, r.n-first)
	for i := first; i < r.n; i++ {
		out = append(out, r.at(i))
	}
	return out
}

// Query returns the newest f.Limit matching entries, oldest first.
func (r *Ring) Query(f LogFilter) []LogEntry {
	if f.Limit <= 0 {
		f.Limit = 200
	}
	min := levelRank[strings.ToUpper(f.MinLevel)]
	tag := strings.ToLower(f.Tag)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []LogEntry
	for i := r.n - 1; i >= 0 && len(out) < f.Limit; i-- {
		e := r.at(i)
		if !f.Since.IsZero() && e.At.Before(f.Since) {
			break
		}
		if min > 0 && levelRank[e.Level] < min {
			continue
		}
		if len(f.PIDs) > 0 && !slices.Contains(f.PIDs, e.PID) {
			continue
		}
		if tag != "" && !strings.Contains(strings.ToLower(e.Tag), tag) {
			continue
		}
		if f.Grep != nil && !f.Grep.MatchString(e.Tag+": "+e.Msg) {
			continue
		}
		out = append(out, e)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (r *Ring) Clear() {
	r.mu.Lock()
	r.start, r.n = 0, 0
	r.mu.Unlock()
}

// Format renders entries in threadtime-like form.
func Format(entries []LogEntry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Time)
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(e.PID))
		b.WriteByte(' ')
		b.WriteString(e.Level)
		b.WriteByte(' ')
		b.WriteString(e.Tag)
		b.WriteString(": ")
		b.WriteString(e.Msg)
		b.WriteByte('\n')
	}
	return b.String()
}
