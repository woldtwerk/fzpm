// Package drupal implements a provider for drupal.org modules.
package drupal

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"fzpm/internal/provider"
)

const (
	baseURL     = "https://www.drupal.org"
	releasesURL = "https://updates.drupal.org/release-history/%s/current"
)

var (
	validID         = regexp.MustCompile(`^[a-z0-9_]+$`)
	validConstraint = regexp.MustCompile(`^[0-9A-Za-z.\-^~|<>=*@ ]*$`)
)

func init() { provider.Register(Drupal{}) }

// Drupal is the drupal.org module provider.
type Drupal struct{}

func (Drupal) Name() string { return "drupal" }

// Icon is Nerd Fonts' nf-dev-drupal in Drupal blue.
func (Drupal) Icon() (string, string) { return "\ue742", "#0678be" }

// Detect walks up from dir looking for a composer.json that requires Drupal
// core. A sibling .ddev directory switches commands to ddev.
func (Drupal) Detect(dir string) (provider.Project, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		if isDrupalComposer(filepath.Join(d, "composer.json")) {
			p := provider.Project{Root: d, Data: map[string]string{}}
			kind := "composer"
			if fi, err := os.Stat(filepath.Join(d, ".ddev")); err == nil && fi.IsDir() {
				p.Data["ddev"] = "1"
				kind = "ddev"
			}
			p.Label = kind + ": " + shortPath(d)
			return p, true
		}
		if parent := filepath.Dir(d); parent == d {
			return provider.Project{}, false
		}
	}
}

func isDrupalComposer(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var c struct {
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(b, &c) != nil {
		return false
	}
	for name := range c.Require {
		if name == "drupal/core" || strings.HasPrefix(name, "drupal/core-") {
			return true
		}
	}
	return false
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
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
	pkg := "drupal/" + id
	if version != "" {
		pkg += ":" + version
	}
	return "composer require '" + pkg + "'"
}

func requireCmd(ps []provider.Package, version string, proj provider.Project) (*exec.Cmd, error) {
	if len(ps) == 0 {
		return nil, fmt.Errorf("no modules to install")
	}
	if proj.Root == "" {
		return nil, fmt.Errorf("no Drupal project (composer.json requiring drupal/core) found from the current directory; run: %s", requireString(ps[0].ID, version))
	}
	if !validConstraint.MatchString(version) {
		return nil, fmt.Errorf("invalid version constraint %q", version)
	}
	args := []string{"require"}
	for _, p := range ps {
		if !validID.MatchString(p.ID) {
			return nil, fmt.Errorf("invalid module name %q", p.ID)
		}
		pkg := "drupal/" + p.ID
		if version != "" {
			pkg += ":" + version
		}
		args = append(args, pkg)
	}
	return tool(proj, "composer", args...), nil
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
	return tool(proj, "drush", args...)
}

func tool(proj provider.Project, name string, args ...string) *exec.Cmd {
	var c *exec.Cmd
	if proj.Data["ddev"] != "" {
		c = exec.Command("ddev", append([]string{name}, args...)...)
	} else {
		c = exec.Command(name, args...)
	}
	c.Dir = proj.Root
	return c
}
