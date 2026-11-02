package typo3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"fzpm/internal/provider"
	"fzpm/internal/provider/composer"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectAndActions(t *testing.T) {
	root := t.TempDir()
	write(t, root, "composer.json", `{"require":{"typo3/cms-core":"^13.4"},"config":{"bin-dir":"bin"}}`)
	os.Mkdir(filepath.Join(root, ".ddev"), 0o755)
	sub := filepath.Join(root, "packages", "site")
	os.MkdirAll(sub, 0o755)

	proj, ok := TYPO3{}.Detect(sub)
	if !ok || proj.Root != root || proj.Data["ddev"] != "1" {
		t.Fatalf("detect = %+v, %v", proj, ok)
	}
	ps := []provider.Package{{ID: "georgringer/news"}, {ID: "in2code/powermail"}}
	cmds, err := TYPO3{}.Actions()[1].Commands(ps, "", proj)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{strings.Join(cmds[0].Args, " "), strings.Join(cmds[1].Args, " ")}
	want := []string{"ddev composer require georgringer/news in2code/powermail", "ddev exec bin/typo3 extension:setup"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("commands = %q, want %q", got, want)
	}
	if _, err := (TYPO3{}).Actions()[0].Commands([]provider.Package{{ID: "bad name; rm"}}, "", proj); err == nil {
		t.Error("invalid package name accepted")
	}

	if _, ok := (TYPO3{}).Detect(t.TempDir()); ok {
		t.Error("detected a project without composer.json")
	}
}

func TestLocalAndRemove(t *testing.T) {
	root := t.TempDir()
	write(t, root, "composer.json", `{"require":{"typo3/cms-core":"^13.4"}}`)
	write(t, root, "composer.lock", `{"packages":[
		{"name":"typo3/cms-core","version":"v13.4.1","type":"typo3-cms-framework","extra":{"typo3/cms":{"extension-key":"core"}}},
		{"name":"typo3/cms-felogin","version":"v13.4.1","type":"typo3-cms-framework","extra":{"typo3/cms":{"extension-key":"felogin"}}},
		{"name":"georgringer/news","version":"12.1.0","type":"typo3-cms-extension","extra":{"typo3/cms":{"extension-key":"news"}}},
		{"name":"acme/sitepackage","version":"dev-main","type":"typo3-cms-extension","dist":{"type":"path"}},
		{"name":"symfony/console","version":"v7.1.0","type":"library"}]}`)
	proj, _ := TYPO3{}.Detect(root)
	lps, err := TYPO3{}.Local(context.Background(), nil, proj)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]provider.LocalPackage{}
	for _, lp := range lps {
		byName[lp.Name] = lp
	}
	if len(lps) != 4 {
		t.Fatalf("local = %+v", lps)
	}
	if lp := byName["georgringer/news"]; lp.Module != "news" || lp.Origin != "contrib" || !lp.Enabled || lp.ID != "georgringer/news" {
		t.Errorf("news = %+v", lp)
	}
	if lp := byName["typo3/cms-felogin"]; lp.Origin != "core" {
		t.Errorf("felogin = %+v", lp)
	}
	if lp := byName["acme/sitepackage"]; lp.Origin != "custom" || lp.ID != "" || lp.Module != "sitepackage" {
		t.Errorf("sitepackage = %+v", lp)
	}

	cmds, err := TYPO3{}.RemoveCommands([]provider.LocalPackage{byName["georgringer/news"], byName["typo3/cms-felogin"]}, proj)
	if err != nil || strings.Join(cmds[0].Args, " ") != "composer remove georgringer/news typo3/cms-felogin" {
		t.Errorf("remove = %v, %v", cmds, err)
	}
	for _, name := range []string{"typo3/cms-core", "acme/sitepackage"} {
		if _, err := (TYPO3{}).RemoveCommands([]provider.LocalPackage{byName[name]}, proj); err == nil {
			t.Errorf("removing %s accepted", name)
		}
	}
}

func TestIndexSearchDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/packages/list.json":
			if r.URL.Query().Get("type") != "typo3-cms-extension" {
				t.Errorf("list type = %q", r.URL.RawQuery)
			}
			w.Write([]byte(`{"packages":{"georgringer/news":{"abandoned":false},"old/ext":{"abandoned":"new/ext"},"tiny/ext":{"abandoned":false}}}`))
		case "/search.json":
			if r.URL.Query().Get("page") == "1" {
				w.Write([]byte(`{"results":[{"name":"georgringer/news","description":"News system","downloads":5000}]}`))
				return
			}
			w.Write([]byte(`{"results":[]}`))
		case "/packages/georgringer/news.json":
			w.Write([]byte(`{"package":{"name":"georgringer/news","description":"News system","repository":"https://gitlab.com/georgringer/news",
				"maintainers":[{"name":"georgringer","avatar_url":"https://avatar"}],"downloads":{"total":5799016,"monthly":128794},"github_stars":287,
				"versions":{
					"dev-main":{"version":"dev-main","time":"2026-10-01T00:00:00+00:00"},
					"v12.1.0":{"version":"v12.1.0","time":"2026-08-13T10:55:59+00:00","license":["GPL-2.0-or-later"],
						"require":{"typo3/cms-core":"^13.4.29 || ^14.3"},"extra":{"typo3/cms":{"extension-key":"news"}},
						"support":{"docs":"https://docs.typo3.org/p/georgringer/news/main/en-us/","issues":"https://github.com/georgringer/news/issues"}},
					"v12.0.0-beta1":{"version":"v12.0.0-beta1","time":"2026-07-01T00:00:00+00:00"}}}}`))
		default:
			http.NotFound(w, r) // README
		}
	}))
	defer srv.Close()
	old := composer.PackagistURL
	composer.PackagistURL = srv.URL
	defer func() { composer.PackagistURL = old }()
	ctx, c := context.Background(), srv.Client()

	var mu sync.Mutex
	index := map[string]provider.Package{}
	err := TYPO3{}.Index(ctx, c, func(ps []provider.Package) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range ps {
			if p.Summary != "" || index[p.ID].Summary == "" {
				index[p.ID] = p
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 || index["georgringer/news"].Popularity != 5000 || index["tiny/ext"].Provider != "typo3" {
		t.Errorf("index = %+v", index)
	}

	ps, err := TYPO3{}.Search(ctx, c, "news")
	if err != nil || len(ps) != 1 || ps[0].Summary != "News system" {
		t.Errorf("search = %+v, %v", ps, err)
	}

	d, err := TYPO3{}.Detail(ctx, c, "georgringer/news")
	if err != nil {
		t.Fatal(err)
	}
	if d.Popularity != 128794 || d.Body != "News system" || len(d.Releases) != 2 {
		t.Fatalf("detail = %+v", d)
	}
	if r := d.Releases[0]; r.Version != "v12.1.0" || r.Constraint != "^12.1.0" || r.Compat != "^13.4.29 || ^14.3" {
		t.Errorf("release = %+v", r)
	}
	if r := d.Releases[1]; r.Constraint != "12.0.0-beta1" {
		t.Errorf("pre-release = %+v", r)
	}
	var labels []string
	for _, s := range d.Sidebar {
		for _, l := range s.Items {
			labels = append(labels, l.Label+"="+l.URL)
		}
	}
	all := strings.Join(labels, "\n")
	for _, want := range []string{"Extension key: news=", "TYPO3 Extension Repository=https://extensions.typo3.org/extension/news", "georgringer=" + srv.URL + "/users/georgringer/", "Issues=https://github.com/georgringer/news/issues"} {
		if !strings.Contains(all, want) {
			t.Errorf("sidebar misses %q:\n%s", want, all)
		}
	}
}
