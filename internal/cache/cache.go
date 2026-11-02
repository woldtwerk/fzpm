// Package cache is a small disk cache with TTLs and an in-memory layer.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Store caches byte blobs below a directory. Keys are slash separated; the
// last segment is hashed if it contains unsafe characters.
type Store struct {
	dir string
	mu  sync.RWMutex
	mem map[string]entry
}

type entry struct {
	b   []byte
	mod time.Time
}

// Open opens (and creates) the cache at $XDG_CACHE_HOME/<app>.
func Open(app string) (*Store, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return OpenDir(filepath.Join(base, app))
}

// OpenDir opens a cache rooted at dir.
func OpenDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, mem: map[string]entry{}}, nil
}

var safe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

func (s *Store) path(key string) string {
	dir, file := filepath.Split(filepath.FromSlash(key))
	if !safe.MatchString(file) {
		sum := sha256.Sum256([]byte(file))
		file = hex.EncodeToString(sum[:16])
	}
	return filepath.Join(s.dir, filepath.Clean(dir), file)
}

// Get returns the cached value if it exists and is younger than ttl
// (ttl <= 0 means it never expires).
func (s *Store) Get(key string, ttl time.Duration) ([]byte, bool) {
	b, age, ok := s.GetAny(key)
	if !ok || (ttl > 0 && age > ttl) {
		return nil, false
	}
	return b, true
}

// GetAny returns the cached value regardless of age, plus its age.
func (s *Store) GetAny(key string) ([]byte, time.Duration, bool) {
	s.mu.RLock()
	e, ok := s.mem[key]
	s.mu.RUnlock()
	if !ok {
		p := s.path(key)
		fi, err := os.Stat(p)
		if err != nil {
			return nil, 0, false
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, 0, false
		}
		e = entry{b, fi.ModTime()}
		s.mu.Lock()
		s.mem[key] = e
		s.mu.Unlock()
	}
	return e.b, time.Since(e.mod), true
}

// Put stores a value in memory and on disk (atomically).
func (s *Store) Put(key string, b []byte) error {
	s.mu.Lock()
	s.mem[key] = entry{b, time.Now()}
	s.mu.Unlock()
	p := s.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Age returns how old the on-disk entry is.
func (s *Store) Age(key string) (time.Duration, bool) {
	fi, err := os.Stat(s.path(key))
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

// GetJSON decodes a cached JSON value.
func (s *Store) GetJSON(key string, ttl time.Duration, v any) bool {
	b, ok := s.Get(key, ttl)
	return ok && json.Unmarshal(b, v) == nil
}

// PutJSON encodes and stores a JSON value.
func (s *Store) PutJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Put(key, b)
}
