// Package audit records every MCP tool call: in memory for the UI and as
// daily JSONL files under %LOCALAPPDATA%\adbridge\logs.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

type Entry struct {
	Seq        uint64    `json:"seq"`
	Time       time.Time `json:"time"`
	Origin     string    `json:"origin"` // local | remote
	RemoteAddr string    `json:"remoteAddr,omitempty"`
	Client     string    `json:"client,omitempty"`
	Tool       string    `json:"tool"`
	Device     string    `json:"device,omitempty"`
	Args       string    `json:"args"`
	DurationMs int64     `json:"durationMs"`
	OK         bool      `json:"ok"`
	Denied     bool      `json:"denied,omitempty"`
	Error      string    `json:"error,omitempty"`
	Summary    string    `json:"summary,omitempty"`
}

const keep = 1000

type Log struct {
	dir string

	mu      sync.Mutex
	entries []Entry
	seq     uint64
	subs    []func(Entry)
	file    *os.File
	day     string
}

// New writes JSONL into dir ("" = memory only).
func New(dir string) *Log { return &Log{dir: dir} }

func (l *Log) OnEntry(fn func(Entry)) {
	l.mu.Lock()
	l.subs = append(l.subs, fn)
	l.mu.Unlock()
}

var secretRe = regexp.MustCompile(`(?i)((?:token|authtoken|password|secret|bearer)["'=: ]+)[^\s"',}]+`)

// Redact masks obvious secrets in free text.
func Redact(s string) string { return secretRe.ReplaceAllString(s, "${1}***") }

func (l *Log) Add(e Entry) Entry {
	e.Args = Redact(truncate(e.Args, 2000))
	e.Error = truncate(e.Error, 1000)
	e.Summary = truncate(e.Summary, 300)
	l.mu.Lock()
	l.seq++
	e.Seq = l.seq
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.entries = append(l.entries, e)
	if len(l.entries) > keep {
		l.entries = append([]Entry(nil), l.entries[len(l.entries)-keep:]...)
	}
	l.writeLocked(e)
	subs := append([]func(Entry){}, l.subs...)
	l.mu.Unlock()
	for _, f := range subs {
		f(e)
	}
	return e
}

// Since returns entries with Seq > seq.
func (l *Log) Since(seq uint64) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Entry
	for _, e := range l.entries {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out
}

// Last returns the most recent entry.
func (l *Log) Last() (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return Entry{}, false
	}
	return l.entries[len(l.entries)-1], true
}

func (l *Log) writeLocked(e Entry) {
	if l.dir == "" {
		return
	}
	day := e.Time.Format("2006-01-02")
	if l.file == nil || l.day != day {
		if l.file != nil {
			l.file.Close()
		}
		_ = os.MkdirAll(l.dir, 0o755)
		f, err := os.OpenFile(filepath.Join(l.dir, "audit-"+day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			l.file = nil
			return
		}
		l.file, l.day = f, day
	}
	b, _ := json.Marshal(e)
	l.file.Write(append(b, '\n'))
}

func (l *Log) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
