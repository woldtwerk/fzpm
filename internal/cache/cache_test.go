package cache

import (
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutJSON("drupal/detail/token", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("img/https://example.com/a.png?x=1", []byte("png")); err != nil {
		t.Fatal(err)
	}
	// Fresh store reads from disk.
	s2, _ := OpenDir(dir)
	var v map[string]int
	if !s2.GetJSON("drupal/detail/token", time.Hour, &v) || v["a"] != 1 {
		t.Errorf("json roundtrip failed: %v", v)
	}
	if b, ok := s2.Get("img/https://example.com/a.png?x=1", 0); !ok || string(b) != "png" {
		t.Errorf("bytes roundtrip failed")
	}
	if _, ok := s2.Get("drupal/detail/token", time.Nanosecond); ok {
		t.Errorf("expected expiry")
	}
}
