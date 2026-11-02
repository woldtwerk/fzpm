// Package drupal implements a provider for drupal.org modules.
package drupal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"fzpm/internal/provider"
	"fzpm/internal/provider/composer"
)

const (
	baseURL     = "https://www.drupal.org"
	releasesURL = "https://updates.drupal.org/release-history/%s/current"
)

var validID = regexp.MustCompile(`^[a-z0-9_]+$`)

func init() { provider.Register(Drupal{}) }

// Drupal is the drupal.org module provider.
type Drupal struct{}

func (Drupal) Name() string { return "drupal" }

// Icon is Nerd Fonts' nf-dev-drupal in Drupal blue.
func (Drupal) Icon() (string, string) { return "\ue742", "#0678be" }

// Detect walks up from dir looking for a composer.json that requires Drupal
// core. A sibling .ddev directory switches commands to ddev.
func (Drupal) Detect(dir string) (provider.Project, bool) {
	return composer.Find(dir, func(c composer.JSON) bool {
		for name := range c.Require {
			if name == "drupal/core" || strings.HasPrefix(name, "drupal/core-") {
				return true
			}
		}
		return false
	})
}

// Actions: [0] composer require, [1] composer require + drush en. Several
// modules go into a single composer require and a single drush en.
func (Drupal) Actions() []provider.Action {
	return []provider.Action{
		{Label: "install", Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			req, err := requireCmd(ps, v, proj)
			if err != nil {
				return nil, err
			}
			return []*exec.Cmd{req}, nil
		}},
		{Label: "install + enable", Enables: true, Commands: func(ps []provider.Package, v string, proj provider.Project) ([]*exec.Cmd, error) {
			req, err := requireCmd(ps, v, proj)
			if err != nil {
				return nil, err
			}
			ids := make([]string, len(ps))
			for i, p := range ps {
				ids[i] = p.ID
			}
			return []*exec.Cmd{req, drushEn(proj, ids)}, nil
		}},
	}
}

func (Drupal) InstallHint(p provider.Package, version string) string {
	return requireString(p.ID, version)
}

func requireString(id, version string) string {
	return "composer require '" + composer.Spec("drupal/"+id, version) + "'"
}

func requireCmd(ps []provider.Package, version string, proj provider.Project) (*exec.Cmd, error) {
	if len(ps) == 0 {
		return nil, fmt.Errorf("no modules to install")
	}
	if proj.Root == "" {
		return nil, fmt.Errorf("no Drupal project (composer.json requiring drupal/core) found from the current directory; run: %s", requireString(ps[0].ID, version))
	}
	if !composer.ValidConstraint.MatchString(version) {
		return nil, fmt.Errorf("invalid version constraint %q", version)
	}
	args := []string{"require"}
	for _, p := range ps {
		if !validID.MatchString(p.ID) {
			return nil, fmt.Errorf("invalid module name %q", p.ID)
		}
		args = append(args, composer.Spec("drupal/"+p.ID, version))
	}
	return composer.Command(proj, args...), nil
}

// drushEn enables modules (validated by the caller).
func drushEn(proj provider.Project, modules []string) *exec.Cmd {
	return drushCmd(proj, append(append([]string{"en"}, modules...), "-y")...)
}

func drushCmd(proj provider.Project, args ...string) *exec.Cmd {
	if proj.Data["ddev"] == "" {
		local := filepath.Join(proj.Root, "vendor", "bin", "drush")
		if _, err := os.Stat(local); err == nil {
			c := exec.Command(local, args...)
			c.Dir = proj.Root
			return c
		}
	}
	return composer.Exec(proj, "drush", args...)
}
