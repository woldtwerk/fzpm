// Package npm holds what providers built on the npm registry share:
// registry documents, download counts, versions and the local project's
// package manager.
package npm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"fzpm/internal/provider"
)

// Endpoints (variables so tests can point them at a local server).
var (
	RegistryURL  = "https://registry.npmjs.org/"
	DownloadsURL = "https://npmx.dev/api/registry/downloads/versions?packages="
)

// Doc is a package document of the npm registry.
type Doc struct {
	Name     string            `json:"name"`
	DistTags map[string]string `json:"dist-tags"`
	Time     map[string]string `json:"time"`
	Versions map[string]struct {
		Deprecated json.RawMessage `json:"deprecated"`
	} `json:"versions"`
	Readme      string          `json:"readme"`
	Description string          `json:"description"`
	Homepage    string          `json:"homepage"`
	License     json.RawMessage `json:"license"`
	Repository  json.RawMessage `json:"repository"`
	Bugs        json.RawMessage `json:"bugs"`
	Maintainers []struct {
		Name string `json:"name"`
	} `json:"maintainers"`
}

// Download counts come from npmx.dev, which returns weekly downloads for
// several packages per request, scoped ones included (api.npmjs.org's bulk
// endpoint cannot do scoped packages and rate-limits single requests hard).
// npmx answers large batches with 502s, so names are sent in small batches;
// a failing batch is split in half and retried.
const downloadsBatch = 25

// RetryPause is the delay before retrying a failed batch (variable for tests).
var RetryPause = time.Second

// WeeklyDownloads reports last week's downloads batch by batch; packages
// that keep failing are left out.
func WeeklyDownloads(ctx context.Context, c *http.Client, names []string, report func(map[string]int64)) {
	for i := 0; i < len(names) && ctx.Err() == nil; i += downloadsBatch {
		fetchDownloads(ctx, c, names[i:min(i+downloadsBatch, len(names))], report, 0)
	}
}

func fetchDownloads(ctx context.Context, c *http.Client, names []string, report func(map[string]int64), depth int) {
	got, err := Downloads(ctx, c, names)
	if err == nil {
		report(got)
		return
	}
	if ctx.Err() != nil || depth >= 3 {
		return
	}
	pause(ctx, RetryPause)
	if len(names) == 1 {
		fetchDownloads(ctx, c, names, report, depth+1)
		return
	}
	half := len(names) / 2
	fetchDownloads(ctx, c, names[:half], report, depth+1)
	fetchDownloads(ctx, c, names[half:], report, depth+1)
}

// Downloads returns last week's downloads for a few packages.
func Downloads(ctx context.Context, c *http.Client, names []string) (map[string]int64, error) {
	r, err := provider.Fetch(ctx, c, DownloadsURL+url.QueryEscape(strings.Join(names, ",")))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var res struct {
		Packages []struct {
			PackageName string `json:"packageName"`
			Versions    []struct {
				Downloads int64 `json:"downloads"`
			} `json:"versions"`
		} `json:"packages"`
	}
	if err := json.NewDecoder(r).Decode(&res); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(res.Packages))
	for _, p := range res.Packages {
		var sum int64
		for _, v := range p.Versions {
			sum += v.Downloads
		}
		out[p.PackageName] = sum
	}
	return out, nil
}

func pause(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// Releases lists published versions, newest first.
func Releases(doc Doc) []provider.Release {
	tags := map[string][]string{}
	for tag, v := range doc.DistTags {
		tags[v] = append(tags[v], tag)
	}
	var out []provider.Release
	for v, ts := range doc.Time {
		if _, ok := doc.Versions[v]; !ok {
			continue // "created", "modified", unpublished
		}
		t, _ := time.Parse(time.RFC3339, ts)
		r := provider.Release{
			Version:    v,
			Constraint: Constraint(v),
			Date:       t,
			URL:        "https://www.npmjs.com/package/" + doc.Name + "/v/" + v,
		}
		sort.Strings(tags[v])
		r.Tags = append(r.Tags, tags[v]...)
		if dep := doc.Versions[v].Deprecated; len(dep) > 0 && string(dep) != "false" {
			r.Tags = append(r.Tags, "deprecated")
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.After(out[j].Date) })
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// Constraint: ^x.y.z for stable versions, the exact version otherwise.
func Constraint(v string) string {
	if strings.ContainsAny(v, "-+") {
		return v
	}
	return "^" + v
}

// StringOrURL reads npm fields that are either "x" or {"url": "x"} /
// {"type": "x"}.
func StringOrURL(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct{ URL, Type string }
	if json.Unmarshal(raw, &o) == nil {
		if o.URL != "" {
			return o.URL
		}
		return o.Type
	}
	return ""
}

// Validation of names and versions passed to commands.
var (
	ValidName    = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)
	ValidVersion = regexp.MustCompile(`^[0-9A-Za-z.^~<>=*|+ -]*$`)
)

// PackageJSON holds the dependencies of a package.json.
type PackageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// Has reports whether name is a (dev) dependency.
func (p PackageJSON) Has(name string) bool {
	_, a := p.Dependencies[name]
	_, b := p.DevDependencies[name]
	return a || b
}

// ReadPackageJSON reads dir/package.json.
func ReadPackageJSON(dir string) (PackageJSON, bool) {
	var p PackageJSON
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return p, false
	}
	return p, true
}

// Find walks up from dir to the first package.json that match accepts; the
// project remembers its package manager in Data["pm"].
func Find(dir string, match func(PackageJSON) bool) (provider.Project, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		if pkg, ok := ReadPackageJSON(d); ok && match(pkg) {
			pm := PackageManager(d)
			return provider.Project{
				Root:  d,
				Label: pm + ": " + provider.ShortPath(d),
				Data:  map[string]string{"pm": pm},
			}, true
		}
		if parent := filepath.Dir(d); parent == d {
			return provider.Project{}, false
		}
	}
}

// PackageManager guesses the package manager from the lockfile.
func PackageManager(dir string) string {
	for _, c := range []struct{ file, pm string }{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"bun.lock", "bun"},
		{"bun.lockb", "bun"},
	} {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			return c.pm
		}
	}
	return "npm"
}

// Spec is name@version, or name for the latest version.
func Spec(name, version string) string {
	if version != "" {
		return name + "@" + version
	}
	return name
}

// InstalledVersion reads the version of name from node_modules.
func InstalledVersion(root, name string) string {
	var p struct {
		Version string `json:"version"`
	}
	b, err := os.ReadFile(filepath.Join(root, "node_modules", name, "package.json"))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p.Version
}

// Fetch loads a package document from the registry.
func Fetch(ctx context.Context, c *http.Client, name string) (Doc, error) {
	var doc Doc
	r, err := provider.Fetch(ctx, c, RegistryURL+strings.Replace(name, "/", "%2F", 1))
	if err != nil {
		return doc, err
	}
	defer r.Close()
	err = json.NewDecoder(r).Decode(&doc)
	return doc, err
}
