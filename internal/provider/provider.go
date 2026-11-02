// Package provider defines the contract between the source-agnostic core
// (UI, cache, search, runner) and concrete package sources such as drupal.org.
//
// Providers register themselves in init() and are activated by a blank import
// in cmd/fzpm, similar to database/sql drivers.
package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// Package is a single search result / index entry.
type Package struct {
	Provider   string    `json:"provider"`
	ID         string    `json:"id"`   // stable machine name, e.g. "token"
	Name       string    `json:"name"` // human title, e.g. "Token"
	Summary    string    `json:"summary"`
	Popularity int64     `json:"popularity"` // installs / downloads
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
}

// Link is a labelled URL shown in the detail sidebar. URL may be empty for
// plain facts (e.g. "590,374 sites").
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Image string `json:"image,omitempty"` // optional avatar/icon URL
}

// Section groups sidebar links, e.g. "Maintainers", "Resources".
type Section struct {
	Title string `json:"title"`
	Items []Link `json:"items"`
}

// Release is one installable version.
type Release struct {
	Version    string    `json:"version"`    // provider-native version, e.g. "8.x-1.18"
	Constraint string    `json:"constraint"` // what to pass to the installer, e.g. "^1.18"
	Date       time.Time `json:"date"`
	Compat     string    `json:"compat"` // e.g. "^10.3 || ^11"
	Tags       []string  `json:"tags"`   // e.g. "security", "Bug fixes"
	URL        string    `json:"url"`
}

// Detail is everything needed to render a package's detail page.
type Detail struct {
	Package
	URL string `json:"url"`
	// PopularityLabel names Popularity, e.g. "installs" (default) or
	// "downloads / month".
	PopularityLabel string    `json:"popularity_label,omitempty"`
	Body            string    `json:"body"` // markdown
	Images          []string  `json:"images"`
	Releases        []Release `json:"releases"`
	Sidebar         []Section `json:"sidebar"`
}

// Project is a detected local project a provider can install into.
type Project struct {
	Root  string // directory containing the manifest
	Label string // short UI label, e.g. "ddev: ~/site"
	Data  map[string]string
}

// Action is an install-like operation bound to a key (first action = "i",
// second action = "I").
type Action struct {
	Label string
	// Enables marks actions that also enable the package in the project, so
	// the UI can show it as installed before the project files catch up.
	Enables bool
	// Commands returns the commands to run in order for one or more packages
	// of this provider (several when the user selected multiple rows, so the
	// provider can batch them into one install). version may be empty
	// (latest); the UI only sets it for a single package. It must not use a
	// shell.
	Commands func(ps []Package, version string, proj Project) ([]*exec.Cmd, error)
}

// Provider is a package source.
type Provider interface {
	Name() string
	// Index fetches the packages to keep in the local fuzzy index and calls
	// emit for every batch as soon as it arrives. emit is safe to call
	// concurrently.
	Index(ctx context.Context, c *http.Client, emit func([]Package)) error
	// Search performs a remote search (fallback for long-tail packages).
	Search(ctx context.Context, c *http.Client, query string) ([]Package, error)
	// Detail fetches the detail page of a package.
	Detail(ctx context.Context, c *http.Client, id string) (*Detail, error)
	// Detect looks for a project this provider can install into, starting at
	// dir and walking upwards.
	Detect(dir string) (Project, bool)
	// Actions lists the install actions offered for this provider.
	Actions() []Action
	// InstallHint is a copy-pasteable install command (for yanking).
	InstallHint(p Package, version string) string
}

// LocalPackage is a package present in a local project.
type LocalPackage struct {
	// Package identifies the upstream package for detail pages (ID may be
	// empty for custom code); Name is the display name.
	Package
	Module  string `json:"module"` // local machine name
	Group   string `json:"group"`  // e.g. Drupal's package ("Core", "SEO")
	Origin  string `json:"origin"` // "core", "contrib" or "custom"
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
}

// Iconer is optionally implemented by providers that have an icon: a Nerd
// Font glyph and its (brand) color, e.g. "#0678be".
type Iconer interface {
	Icon() (glyph, color string)
}

// Lister is optionally implemented by providers that can list what is in a
// local project (shown in the Installed / Uninstalled tabs).
type Lister interface {
	// Local should work from project files; c is available for registry
	// lookups (e.g. which dependencies are modules at all).
	Local(ctx context.Context, c *http.Client, proj Project) ([]LocalPackage, error)
	// EnableCommands turns one or more present but uninstalled packages on.
	EnableCommands(lps []LocalPackage, proj Project) ([]*exec.Cmd, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]Provider{}
)

// Register makes a provider available. It panics on duplicate names.
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[p.Name()]; dup {
		panic(fmt.Sprintf("provider %q registered twice", p.Name()))
	}
	registry[p.Name()] = p
}

// Get returns a registered provider by name.
func Get(name string) (Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// All returns all registered providers sorted by name.
func All() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Provider, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Fetch GETs a URL and returns the body of a 200 response.
func Fetch(ctx context.Context, c *http.Client, u string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", u, res.Status)
	}
	return res.Body, nil
}
