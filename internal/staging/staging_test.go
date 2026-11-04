package staging

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestChunkedUpload(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	it, err := s.Append("", "../../evil name.apk", []byte("hello "), false)
	if err != nil {
		t.Fatal(err)
	}
	if it.Name != "evil_name.apk" {
		t.Fatalf("name not sanitised: %q", it.Name)
	}
	if _, _, err := s.Get(it.ID); err == nil {
		t.Fatal("incomplete upload should not be usable")
	}
	it, err = s.Append(it.ID, "", []byte("world"), true)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello world"))
	if !it.Complete || it.Size != 11 || it.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("got %+v", it)
	}
	_, path, err := s.Get(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "hello world" {
		t.Fatalf("file content %q", b)
	}
	if _, err := s.Append(it.ID, "", []byte("x"), false); err == nil {
		t.Fatal("append after final should fail")
	}
	if _, err := s.Append("up_missing", "", []byte("x"), false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	s.Delete(it.ID)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("delete left the file behind")
	}
}
