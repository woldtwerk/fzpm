// Package update keeps fzpm current with its GitHub releases: it finds the
// latest release, verifies the archive against the release's checksums and
// replaces the running binary.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Endpoints (variables so tests can point them at a local server).
var (
	latestURL   = "https://api.github.com/repos/woldtwerk/fzpm/releases/latest"
	downloadURL = "https://github.com/woldtwerk/fzpm/releases/download/v%s/%s"
)

const maxArchive = 100 << 20

// Latest returns the version of the newest release, e.g. "1.2.3".
func Latest(ctx context.Context, c *http.Client) (string, error) {
	b, err := get(ctx, c, latestURL, 1<<20)
	if err != nil {
		return "", err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &rel); err != nil {
		return "", err
	}
	v := strings.TrimPrefix(rel.Tag, "v")
	if !IsRelease(v) {
		return "", fmt.Errorf("unexpected release tag %q", rel.Tag)
	}
	return v, nil
}

// IsRelease reports whether v is a release version (x.y.z). Local builds
// ("dev") are not and never update themselves.
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok
}

// Newer reports whether version a is newer than b.
func Newer(a, b string) bool {
	va, okA := parse(a)
	vb, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Asset is the release archive for a platform (see .goreleaser.yaml).
func Asset(goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "fzpm_" + goos + "_" + goarch + ext
}

// Install downloads release version for this platform, checks it against
// the release's checksums.txt and replaces the binary at exe.
func Install(ctx context.Context, c *http.Client, version, exe string) error {
	name := Asset(runtime.GOOS, runtime.GOARCH)
	sums, err := get(ctx, c, fmt.Sprintf(downloadURL, version, "checksums.txt"), 1<<20)
	if err != nil {
		return err
	}
	want, err := checksum(sums, name)
	if err != nil {
		return err
	}
	archive, err := get(ctx, c, fmt.Sprintf(downloadURL, version, name), maxArchive)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s: checksum mismatch", name)
	}
	bin, err := extract(archive, name)
	if err != nil {
		return err
	}
	return replace(exe, bin)
}

// checksum finds name's SHA-256 in a sha256sum style list.
func checksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == name && len(f[0]) == sha256.Size*2 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

// extract returns the fzpm binary from a release archive.
func extract(archive []byte, name string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == "fzpm.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxArchive))
			}
		}
		return nil, errors.New("fzpm.exe not found in " + name)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("fzpm not found in %s: %w", name, err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "fzpm" {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}

// replace swaps exe for bin. The new file is written next to exe and renamed
// over it, so the binary is never half written. Windows cannot overwrite a
// running executable, but it can rename it out of the way.
func replace(exe string, bin []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".fzpm-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), exe)
}

func get(ctx context.Context, c *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}
