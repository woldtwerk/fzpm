package drupal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fzpm/internal/provider"
)

func TestConstraint(t *testing.T) {
	for in, want := range map[string]string{
		"8.x-1.18":    "^1.18",
		"2.0.3":       "^2.0",
		"8.x-1.x-dev": "1.x-dev",
		"3.0.0-beta1": "3.0.0-beta1",
		"2.x-dev":     "2.x-dev",
	} {
		if got := Constraint(in); got != want {
			t.Errorf("Constraint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParsePage(t *testing.T) {
	f, err := os.Open("testdata/token.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := parsePage(f, "token")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Token" {
		t.Errorf("name = %q", d.Name)
	}
	if !strings.Contains(d.Body, "placeholder variables") {
		t.Errorf("body missing description: %q", d.Body)
	}
	if len(d.Images) != 2 || !strings.HasPrefix(d.Images[0], "https://www.drupal.org/files/project-images/") {
		t.Errorf("images = %v", d.Images)
	}
	if d.Popularity < 100000 {
		t.Errorf("popularity = %d", d.Popularity)
	}
	sections := map[string]provider.Section{}
	for _, s := range d.Sidebar {
		sections[s.Title] = s
	}
	for _, title := range []string{"Project information", "Maintainers", "Resources", "Development"} {
		if len(sections[title].Items) == 0 {
			t.Errorf("section %q empty", title)
		}
	}
	if m := sections["Maintainers"].Items[0]; m.Label != "dave reid" || m.URL != "https://www.drupal.org/u/dave-reid" ||
		!strings.Contains(m.Image, "/user-pictures/picture-53892") {
		t.Errorf("first maintainer = %+v", m)
	}
}

func TestParseReleases(t *testing.T) {
	f, err := os.Open("testdata/token.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rels, status, err := parseReleases(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) == 0 || rels[0].Version != "8.x-1.18" || rels[0].Constraint != "^1.18" || rels[0].Compat == "" {
		t.Errorf("first release = %+v", rels[0])
	}
	if len(status) != 2 {
		t.Errorf("status = %v", status)
	}
}

func TestDetectAndActions(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"require":{"drupal/core-recommended":"^11"}}`), 0o644)
	os.Mkdir(filepath.Join(root, ".ddev"), 0o755)
	sub := filepath.Join(root, "web", "modules")
	os.MkdirAll(sub, 0o755)

	d := Drupal{}
	proj, ok := d.Detect(sub)
	if !ok || proj.Root != root || proj.Data["ddev"] == "" {
		t.Fatalf("detect = %+v, %v", proj, ok)
	}
	cmds, err := d.Actions()[1].Commands([]provider.Package{{ID: "token"}}, "^1.18", proj)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{strings.Join(cmds[0].Args, " "), strings.Join(cmds[1].Args, " ")}
	want := []string{"ddev composer require drupal/token:^1.18", "ddev drush en token -y"}
	if got[0] != want[0] || got[1] != want[1] || cmds[0].Dir != root {
		t.Errorf("cmds = %v (dir %s), want %v", got, cmds[0].Dir, want)
	}

	cmds, _ = d.Actions()[1].Commands([]provider.Package{{ID: "token"}, {ID: "pathauto"}}, "", proj)
	got = []string{strings.Join(cmds[0].Args, " "), strings.Join(cmds[1].Args, " ")}
	want = []string{"ddev composer require drupal/token drupal/pathauto", "ddev drush en token pathauto -y"}
	if len(cmds) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("batch cmds = %v, want %v", got, want)
	}
	cmds, _ = d.EnableCommands([]provider.LocalPackage{{Module: "token"}, {Module: "token_view"}}, proj)
	if got := strings.Join(cmds[0].Args, " "); got != "ddev drush en token token_view -y" {
		t.Errorf("batch enable = %q", got)
	}
	tokenMods := []provider.LocalPackage{
		{Package: provider.Package{ID: "token"}, Module: "token", Origin: "contrib"},
		{Package: provider.Package{ID: "token"}, Module: "token_view", Origin: "contrib"},
	}
	cmds, _ = d.DisableCommands(tokenMods, proj)
	if got := strings.Join(cmds[0].Args, " "); got != "ddev drush pmu token token_view -y" {
		t.Errorf("disable = %q", got)
	}
	cmds, _ = d.RemoveCommands(tokenMods, proj)
	if got := strings.Join(cmds[0].Args, " "); got != "ddev composer remove drupal/token" {
		t.Errorf("remove = %q", got)
	}
	for _, lp := range []provider.LocalPackage{
		{Package: provider.Package{ID: "drupal"}, Module: "node", Origin: "core"},
		{Module: "mine", Origin: "custom"},
	} {
		if _, err := d.RemoveCommands([]provider.LocalPackage{lp}, proj); err == nil {
			t.Errorf("removing %s code must fail", lp.Origin)
		}
	}

	if _, err := d.Actions()[0].Commands([]provider.Package{{ID: "token"}, {ID: "token; rm -rf /"}}, "", proj); err == nil {
		t.Error("expected invalid name error")
	}
	if _, ok := d.Detect(t.TempDir()); ok {
		t.Error("detected project in empty dir")
	}
}

func TestLocalFromFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("composer.json", `{"require":{"drupal/core-recommended":"^11","drupal/token":"^1.18","drupal/webform":"^6.3","drupal/gin_toolbar":"^2","drupal/gin":"^4"},
		"extra":{"drupal-scaffold":{"locations":{"web-root":"web/"}}}}`)
	write("composer.lock", `{"packages":[{"name":"drupal/core","version":"11.2.0"},{"name":"drupal/token","version":"1.18.0","type":"drupal-module"},{"name":"drupal/webform","version":"6.3.0","type":"drupal-module"},{"name":"drupal/gin","version":"4.0.0","type":"drupal-theme"}]}`)
	write("config/sync/core.extension.yml", "module:\n  node: 0\n  token: 0\n  my_site: 0\ntheme:\n  olivero: 0\nprofile: standard\n")
	write("web/core/modules/node/node.info.yml", "name: Node\ntype: module\npackage: Core\n")
	write("web/core/modules/ban/ban.info.yml", "name: Ban\ntype: module\npackage: Core\n")
	write("web/core/modules/node/tests/modules/node_test/node_test.info.yml", "name: Node test\ntype: module\n")
	write("web/core/modules/hidden_mod/hidden_mod.info.yml", "name: Hidden\ntype: module\nhidden: true\n")
	write("web/modules/contrib/token/token.info.yml", "name: Token\ntype: module\n")
	write("web/modules/contrib/webform/modules/webform_ui/webform_ui.info.yml", "name: Webform UI\ntype: module\npackage: Webform\n")
	write("web/modules/custom/my_site/my_site.info.yml", "name: My Site\ntype: module\npackage: Custom\n")
	write("web/themes/contrib/gin/gin.info.yml", "name: Gin\ntype: theme\n")

	list, err := Drupal{}.Local(context.Background(), nil, provider.Project{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]provider.LocalPackage{}
	for _, lp := range list {
		got[lp.Module] = lp
	}
	check := func(mod, id, origin, version string, enabled bool) {
		t.Helper()
		lp, ok := got[mod]
		if !ok || lp.ID != id || lp.Origin != origin || lp.Version != version || lp.Enabled != enabled {
			t.Errorf("%s = %+v (present=%v)", mod, lp, ok)
		}
	}
	check("node", "drupal", "core", "11.2.0", true)
	check("ban", "drupal", "core", "11.2.0", false)
	check("token", "token", "contrib", "1.18.0", true)
	check("webform_ui", "webform", "contrib", "6.3.0", false)
	check("my_site", "", "custom", "", true)
	check("gin_toolbar", "gin_toolbar", "contrib", "", false) // required, not downloaded
	for _, skip := range []string{"node_test", "hidden_mod", "gin", "webform"} {
		if _, ok := got[skip]; ok {
			t.Errorf("%s should not be listed", skip)
		}
	}
}
