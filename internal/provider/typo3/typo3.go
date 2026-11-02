// Package typo3 implements a provider for TYPO3 extensions. Extensions are
// Composer packages of type "typo3-cms-extension" on packagist.org; only
// Composer-mode projects are supported, where every installed extension is
// active.
package typo3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"fzpm/internal/provider"
	"fzpm/internal/provider/composer"
)

const (
	extensionType = "typo3-cms-extension"
	frameworkType = "typo3-cms-framework" // system extensions (typo3/cms-*)
	core          = "typo3/cms-core"
)

func init() { provider.Register(TYPO3{}) }

// TYPO3 is the TYPO3 extension provider. Package IDs are Composer names
// ("georgringer/news").
type TYPO3 struct{}

func (TYPO3) Name() string { return "typo3" }

// Icon is Nerd Fonts' nf-dev-typo3 in TYPO3 orange.
func (TYPO3) Icon() (string, string) { return "\ue772", "#ff8700" }

// Detect walks up from dir looking for a composer.json requiring TYPO3 core.
func (TYPO3) Detect(dir string) (provider.Project, bool) {
	return composer.Find(dir, func(c composer.JSON) bool {
		return c.Requires(core) || c.Requires("typo3/minimal")
	})
}

// Index: Packagist's search only pages through a few hundred hits, so the
// index holds every extension's name (one request), then the most popular
// ones with description and downloads. Remote search covers the rest.
func (TYPO3) Index(ctx context.Context, c *http.Client, emit func([]provider.Package)) error {
	names, err := composer.List(ctx, c, extensionType)
	if err != nil {
		return err
	}
	all := make([]provider.Package, len(names))
	for i, n := range names {
		all[i] = provider.Package{Provider: "typo3", ID: n, Name: n}
	}
	emit(all)
	g, ctx := errgroup.WithContext(ctx)
	for page := 1; page <= 3; page++ {
		g.Go(func() error {
			ps, err := composer.Search(ctx, c, "typo3", "", extensionType, page)
			if err == nil {
				emit(ps)
			}
			return err
		})
	}
	return g.Wait()
}

// Search uses packagist.org's search, limited to TYPO3 extensions.
func (TYPO3) Search(ctx context.Context, c *http.Client, query string) ([]provider.Package, error) {
	return composer.Search(ctx, c, "typo3", query, extensionType, 1)
}

// Detail is the Packagist page plus the extension key and TER link.
func (TYPO3) Detail(ctx context.Context, c *http.Client, id string) (*provider.Detail, error) {
	p, err := composer.Fetch(ctx, c, id)
	if err != nil {
		return nil, err
	}
	d := composer.Detail(ctx, c, "typo3", p, core)
	if key := extensionKey(p.Latest().Extra); key != "" {
		d.Sidebar[0].Items = append(d.Sidebar[0].Items, provider.Link{Label: "Extension key: " + key})
		ter := provider.Link{Label: "TYPO3 Extension Repository", URL: "https://extensions.typo3.org/extension/" + key}
		for i, s := range d.Sidebar {
			if s.Title != "Resources" {
				continue
			}
			for j, l := range s.Items {
				if l.URL == ter.URL { // the homepage often is the TER page
					s.Items = append(s.Items[:j], s.Items[j+1:]...)
					break
				}
			}
			d.Sidebar[i].Items = append(s.Items, ter)
		}
	}
	return d, nil
}

// extensionKey reads extra.typo3/cms.extension-key.
func extensionKey(extra map[string]json.RawMessage) string {
	var t struct {
		Key string `json:"extension-key"`
	}
	_ = json.Unmarshal(extra["typo3/cms"], &t)
	return t.Key
}

// Actions: [0] composer require, [1] composer require + `typo3
// extension:setup` (database tables, default data). In Composer mode a
// required extension is active right away; there is no separate enable.
func (TYPO3) Actions() []provider.Action {
	return []provider.Action{
		{Label: "install", Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			req, err := requireCmd(ps, v, proj)
			if err != nil {
				return nil, err
			}
			return []*exec.Cmd{req}, nil
		}},
		{Label: "install + set up", Enables: true, Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			req, err := requireCmd(ps, v, proj)
			if err != nil {
				return nil, err
			}
			return []*exec.Cmd{req, setupCmd(proj)}, nil
		}},
	}
}

func (TYPO3) InstallHint(p provider.Package, version string) string {
	return "composer require '" + composer.Spec(p.ID, version) + "'"
}

func requireCmd(ps []provider.Package, version string, proj provider.Project) (*exec.Cmd, error) {
	if len(ps) == 0 {
		return nil, fmt.Errorf("no extensions to install")
	}
	if proj.Root == "" {
		return nil, fmt.Errorf("no TYPO3 project (composer.json requiring %s) found from the current directory; run: composer require '%s'", core, composer.Spec(ps[0].ID, version))
	}
	if !composer.ValidConstraint.MatchString(version) {
		return nil, fmt.Errorf("invalid version constraint %q", version)
	}
	args := []string{"require"}
	for _, p := range ps {
		if !composer.ValidName.MatchString(p.ID) {
			return nil, fmt.Errorf("invalid package name %q", p.ID)
		}
		args = append(args, composer.Spec(p.ID, version))
	}
	return composer.Command(proj, args...), nil
}

// setupCmd runs `typo3 extension:setup` for all active extensions; it is
// idempotent, so extensions set up before are not affected.
func setupCmd(proj provider.Project) *exec.Cmd {
	return composer.Bin(proj, "typo3", "extension:setup")
}

var (
	_ provider.Lister  = TYPO3{}
	_ provider.Remover = TYPO3{}
)

// Local lists the extensions in composer.lock: system extensions (core),
// extensions from path repositories (custom) and the rest (contrib). All of
// them are active (Composer mode).
func (TYPO3) Local(_ context.Context, _ *http.Client, proj provider.Project) ([]provider.LocalPackage, error) {
	if proj.Root == "" {
		return nil, fmt.Errorf("no TYPO3 project")
	}
	var out []provider.LocalPackage
	for _, l := range composer.Lock(proj.Root) {
		if l.Type != extensionType && l.Type != frameworkType {
			continue
		}
		key := extensionKey(l.Extra)
		if key == "" {
			_, key, _ = strings.Cut(l.Name, "/")
		}
		lp := provider.LocalPackage{
			Package: provider.Package{Provider: "typo3", ID: l.Name, Name: l.Name},
			Module:  key,
			Origin:  "contrib",
			Version: l.Version,
			Enabled: true,
		}
		switch {
		case l.Type == frameworkType:
			lp.Origin, lp.Group = "core", "system extension"
		case l.Dist.Type == "path":
			lp.Origin, lp.ID = "custom", ""
		}
		out = append(out, lp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EnableCommands: extensions in composer.lock are always active.
func (TYPO3) EnableCommands([]provider.LocalPackage, provider.Project) ([]*exec.Cmd, error) {
	return nil, fmt.Errorf("TYPO3 extensions are active once installed (Composer mode)")
}

// RemoveCommands removes the extensions' packages with a single composer
// remove. There is no disabled state in Composer mode.
func (TYPO3) RemoveCommands(lps []provider.LocalPackage, proj provider.Project) ([]*exec.Cmd, error) {
	args, seen := []string{"remove"}, map[string]bool{}
	for _, lp := range lps {
		if lp.ID == "" || lp.ID == core {
			return nil, fmt.Errorf("%s cannot be removed with composer", lp.Name)
		}
		if !composer.ValidName.MatchString(lp.ID) {
			return nil, fmt.Errorf("invalid package name %q", lp.ID)
		}
		if !seen[lp.ID] {
			seen[lp.ID] = true
			args = append(args, lp.ID)
		}
	}
	return []*exec.Cmd{composer.Command(proj, args...)}, nil
}
