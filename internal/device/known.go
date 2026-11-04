package device

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Known is what adbridge remembers about a device between runs.
type Known struct {
	ID            string    `json:"id"`
	Alias         string    `json:"alias,omitempty"`
	Model         string    `json:"model,omitempty"`
	AIEnabled     bool      `json:"aiEnabled"`
	Wireless      bool      `json:"wireless"`      // has connected over Wi-Fi before
	LastAddr      string    `json:"lastAddr"`      // last ip:port used
	AutoReconnect bool      `json:"autoReconnect"` // cleared by an explicit disconnect
	LastSeen      time.Time `json:"lastSeen"`
}

type knownStore struct {
	path string
	mu   sync.Mutex
	m    map[string]*Known
}

func openKnown(path string) (*knownStore, error) {
	s := &knownStore{path: path, m: map[string]*Known{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Known
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	for _, k := range list {
		s.m[k.ID] = k
	}
	return s, nil
}

// get returns a copy and whether it exists.
func (s *knownStore) get(id string) (Known, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.m[id]
	if !ok {
		return Known{}, false
	}
	return *k, true
}

func (s *knownStore) all() []Known {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Known, 0, len(s.m))
	for _, k := range s.m {
		out = append(out, *k)
	}
	return out
}

// update mutates (creating if needed) and persists.
func (s *knownStore) update(id string, fn func(*Known)) Known {
	s.mu.Lock()
	k, ok := s.m[id]
	if !ok {
		k = &Known{ID: id, AIEnabled: true}
		s.m[id] = k
	}
	fn(k)
	cp := *k
	s.saveLocked()
	s.mu.Unlock()
	return cp
}

func (s *knownStore) remove(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.saveLocked()
	s.mu.Unlock()
}

func (s *knownStore) saveLocked() {
	if s.path == "" {
		return
	}
	list := make([]*Known, 0, len(s.m))
	for _, k := range s.m {
		list = append(list, k)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}
