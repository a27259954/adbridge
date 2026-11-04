// Package staging holds files uploaded by MCP clients in chunks, so a cloud
// agent can get an APK or data file onto the PC (and from there onto a
// device) using only MCP calls.
//
// Uploads live under %LOCALAPPDATA%\adbridge\uploads, expire after TTL and
// are bounded in size.
package staging

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

const (
	TTL          = time.Hour
	MaxFileBytes = 1 << 30 // 1 GiB per upload
	MaxAllBytes  = 4 << 30 // 4 GiB across uploads
)

// Item describes an upload.
type Item struct {
	ID       string    `json:"upload_id"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Complete bool      `json:"complete"`
	SHA256   string    `json:"sha256,omitempty"`
	Expires  time.Time `json:"expires"`
}

type entry struct {
	Item
	path    string
	hash    hash.Hash
	updated time.Time
}

type Store struct {
	dir string
	mu  sync.Mutex
	m   map[string]*entry
}

// Open uses dir, clearing leftovers from previous runs.
func Open(dir string) (*Store, error) {
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, m: map[string]*entry{}}, nil
}

var nameRe = regexp.MustCompile(`[^A-Za-z0-9._\-]+`)

// ErrNotFound means the upload id is unknown or expired.
var ErrNotFound = errors.New("unknown or expired upload_id")

// Append adds data to upload id ("" starts a new upload named name). final
// marks the upload complete and computes its SHA-256.
func (s *Store) Append(id, name string, data []byte, final bool) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	e := s.m[id]
	if id == "" {
		e = &entry{hash: sha256.New()}
		e.ID = newID()
		e.Name = nameRe.ReplaceAllString(filepath.Base(name), "_")
		if e.Name == "" || e.Name == "." {
			e.Name = "upload.bin"
		}
		e.path = filepath.Join(s.dir, e.ID+"-"+e.Name)
		s.m[e.ID] = e
	} else if e == nil {
		return Item{}, ErrNotFound
	}
	if e.Complete {
		return Item{}, errors.New("upload is already complete; start a new one")
	}
	if e.Size+int64(len(data)) > MaxFileBytes {
		return Item{}, fmt.Errorf("upload exceeds %d bytes", MaxFileBytes)
	}
	if s.totalLocked()+int64(len(data)) > MaxAllBytes {
		return Item{}, errors.New("staging area is full; wait for old uploads to expire")
	}
	f, err := os.OpenFile(e.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Item{}, err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Item{}, err
	}
	e.hash.Write(data)
	e.Size += int64(len(data))
	e.updated = time.Now()
	e.Expires = e.updated.Add(TTL)
	if final {
		e.Complete = true
		e.SHA256 = hex.EncodeToString(e.hash.Sum(nil))
	}
	return e.Item, nil
}

// Get returns a completed upload and its file path.
func (s *Store) Get(id string) (Item, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	e := s.m[id]
	if e == nil {
		return Item{}, "", ErrNotFound
	}
	if !e.Complete {
		return Item{}, "", errors.New("upload is not complete; send the last chunk with final=true")
	}
	return e.Item, e.path, nil
}

// Delete removes an upload.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.m[id]; e != nil {
		os.Remove(e.path)
		delete(s.m, id)
	}
}

func (s *Store) totalLocked() int64 {
	var n int64
	for _, e := range s.m {
		n += e.Size
	}
	return n
}

func (s *Store) gcLocked() {
	now := time.Now()
	for id, e := range s.m {
		if now.After(e.Expires) && !e.updated.IsZero() {
			os.Remove(e.path)
			delete(s.m, id)
		}
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "up_" + hex.EncodeToString(b)
}
