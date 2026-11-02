// Package packagist implements a provider for all Composer packages on
// packagist.org. It steps back in projects a specific Composer-based
// provider (Drupal, TYPO3) detects.
package packagist

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"sort"

	"golang.org/x/sync/errgroup"

	"fzpm/internal/provider"
	"fzpm/internal/provider/composer"
)

// indexPages pages of 100 most downloaded packages are kept locally; the
// long tail comes from packagist.org's search.
const indexPages = 10

func init() { provider.Register(Packagist{}) }

// Packagist is the generic Composer package provider. Package IDs are
// Composer names ("guzzlehttp/guzzle").
type Packagist struct{}

func (Packagist) Name() string { return "packagist" }
func (Packagist) Generic()     {}

// Icon is Nerd Fonts' nf-dev-composer in Composer brown.
func (Packagist) Icon() (string, string) { return "\ue783", "#c57d3c" }

// Detect walks up from dir to the next composer.json.
func (Packagist) Detect(dir string) (provider.Project, bool) {
	return composer.Find(dir, func(composer.JSON) bool { return true })
}

func (Packagist) Index(ctx context.Context, c *http.Client, emit func([]provider.Package)) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for page := 1; page <= indexPages; page++ {
		g.Go(func() error {
			ps, err := composer.Popular(ctx, c, "packagist", page)
			if err == nil {
				emit(ps)
			}
			return err
		})
	}
	return g.Wait()
}

func (Packagist) Search(ctx context.Context, c *http.Client, query string) ([]provider.Package, error) {
	return composer.Search(ctx, c, "packagist", query, "", 1)
}

// Detail shows the required PHP version as release compatibility.
func (Packagist) Detail(ctx context.Context, c *http.Client, id string) (*provider.Detail, error) {
	p, err := composer.Fetch(ctx, c, id)
	if err != nil {
		return nil, err
	}
	return composer.Detail(ctx, c, "packagist", p, "php"), nil
}

// Actions: [0] composer require, [1] composer require --dev.
func (Packagist) Actions() []provider.Action {
	return []provider.Action{
		{Label: "install", Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return require(ps, v, proj)
		}},
		{Label: "install (dev)", Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			return require(ps, v, proj, "--dev")
		}},
	}
}

func (Packagist) InstallHint(p provider.Package, version string) string {
	return "composer require '" + composer.Spec(p.ID, version) + "'"
}

func require(ps []provider.Package, version string, proj provider.Project, flags ...string) ([]*exec.Cmd, error) {
	if len(ps) == 0 {
		return nil, fmt.Errorf("no packages to install")
	}
	if proj.Root == "" {
		return nil, fmt.Errorf("no composer.json found from the current directory; run: composer require '%s'", composer.Spec(ps[0].ID, version))
	}
	if !composer.ValidConstraint.MatchString(version) {
		return nil, fmt.Errorf("invalid version constraint %q", version)
	}
	args := append([]string{"require"}, flags...)
	for _, p := range ps {
		if !composer.ValidName.MatchString(p.ID) {
			return nil, fmt.Errorf("invalid package name %q", p.ID)
		}
		args = append(args, composer.Spec(p.ID, version))
	}
	return []*exec.Cmd{composer.Command(proj, args...)}, nil
}

var (
	_ provider.Lister  = Packagist{}
	_ provider.Remover = Packagist{}
)

// Local lists the packages required in composer.json (platform requirements
// like php or ext-* left out), with versions from composer.lock.
func (Packagist) Local(_ context.Context, _ *http.Client, proj provider.Project) ([]provider.LocalPackage, error) {
	c, ok := composer.Read(proj.Root)
	if !ok {
		return nil, fmt.Errorf("no composer.json in %s", proj.Root)
	}
	versions := map[string]string{}
	for _, l := range composer.Lock(proj.Root) {
		versions[l.Name] = l.Version
	}
	var out []provider.LocalPackage
	add := func(reqs map[string]string, group string) {
		for name, constraint := range reqs {
			if !composer.ValidName.MatchString(name) {
				continue
			}
			v := versions[name]
			if v == "" {
				v = constraint
			}
			out = append(out, provider.LocalPackage{
				Package: provider.Package{Provider: "packagist", ID: name, Name: name},
				Module:  name,
				Group:   group,
				Origin:  "packagist",
				Version: v,
				Enabled: true,
			})
		}
	}
	add(c.Require, "require")
	add(c.RequireDev, "require-dev")
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EnableCommands: required packages are always in use.
func (Packagist) EnableCommands([]provider.LocalPackage, provider.Project) ([]*exec.Cmd, error) {
	return nil, fmt.Errorf("Composer packages are in use once installed")
}

// RemoveCommands removes the packages with a single composer remove.
func (Packagist) RemoveCommands(lps []provider.LocalPackage, proj provider.Project) ([]*exec.Cmd, error) {
	args := []string{"remove"}
	for _, lp := range lps {
		if !composer.ValidName.MatchString(lp.ID) {
			return nil, fmt.Errorf("invalid package name %q", lp.ID)
		}
		args = append(args, lp.ID)
	}
	return []*exec.Cmd{composer.Command(proj, args...)}, nil
}
