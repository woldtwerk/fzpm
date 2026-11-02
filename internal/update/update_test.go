package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.1.10", "0.1.9", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"0.2.0", "dev", false},
		{"garbage", "0.1.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("archive fixture is tar.gz")
	}
	archive := tarGz(t, "fzpm", []byte("NEW BINARY"))
	sum := sha256.Sum256(archive)
	asset := Asset(runtime.GOOS, runtime.GOARCH)
	sums := fmt.Sprintf("%s  %s\n%s  other.tar.gz\n", hex.EncodeToString(sum[:]), asset, strings.Repeat("0", 64))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			w.Write([]byte(`{"tag_name":"v0.3.0"}`))
		case "/v0.3.0/checksums.txt":
			w.Write([]byte(sums))
		case "/v0.3.0/" + asset:
			w.Write(archive)
		case "/v0.4.0/checksums.txt":
			w.Write([]byte(strings.Replace(sums, hex.EncodeToString(sum[:]), strings.Repeat("a", 64), 1)))
		case "/v0.4.0/" + asset:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	latestURL, downloadURL = srv.URL+"/latest", srv.URL+"/v%s/%s"

	v, err := Latest(context.Background(), srv.Client())
	if err != nil || v != "0.3.0" {
		t.Fatalf("Latest = %q, %v", v, err)
	}
	exe := filepath.Join(t.TempDir(), "fzpm")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	if err := Install(context.Background(), srv.Client(), v, exe); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(exe)
	fi, _ := os.Stat(exe)
	if string(b) != "NEW BINARY" || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("binary = %q mode %v", b, fi.Mode())
	}
	// A checksum mismatch leaves the binary alone.
	os.WriteFile(exe, []byte("OLD"), 0o755)
	if err := Install(context.Background(), srv.Client(), "0.4.0", exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("mismatch err = %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Errorf("binary replaced despite mismatch: %q", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(exe)); len(entries) != 1 {
		t.Errorf("leftover temp files: %v", entries)
	}
}
