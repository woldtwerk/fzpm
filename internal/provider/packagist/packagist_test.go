package packagist

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

func TestActionsLocalRemove(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"require":{"php":">=8.2","ext-json":"*","guzzlehttp/guzzle":"^7"},"require-dev":{"phpunit/phpunit":"^11"}}`), 0o644)
	os.WriteFile(filepath.Join(root, "composer.lock"), []byte(`{"packages":[{"name":"guzzlehttp/guzzle","version":"7.9.2"}],"packages-dev":[{"name":"phpunit/phpunit","version":"11.4.0"}]}`), 0o644)
	proj, ok := Packagist{}.Detect(root)
	if !ok {
		t.Fatal("not detected")
	}
	ps := []provider.Package{{ID: "monolog/monolog"}}
	cmds, err := Packagist{}.Actions()[1].Commands(ps, "^3.0", proj)
	if err != nil || strings.Join(cmds[0].Args, " ") != "composer require --dev monolog/monolog:^3.0" {
		t.Errorf("dev require = %v, %v", cmds, err)
	}

	lps, err := Packagist{}.Local(context.Background(), nil, proj)
	if err != nil || len(lps) != 2 {
		t.Fatalf("local = %+v, %v", lps, err)
	}
	if lps[0].ID != "guzzlehttp/guzzle" || lps[0].Version != "7.9.2" || lps[1].Group != "require-dev" || lps[1].Version != "11.4.0" {
		t.Errorf("local = %+v", lps)
	}
	cmds, err = Packagist{}.RemoveCommands(lps, proj)
	if err != nil || strings.Join(cmds[0].Args, " ") != "composer remove guzzlehttp/guzzle phpunit/phpunit" {
		t.Errorf("remove = %v, %v", cmds, err)
	}
}

func TestIndexAndSearch(t *testing.T) {
	var pages []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/explore/popular.json":
			mu.Lock()
			pages = append(pages, r.URL.Query().Get("page"))
			mu.Unlock()
			w.Write([]byte(`{"packages":[{"name":"symfony/console","description":"Console","downloads":900}]}`))
		case "/search.json":
			if r.URL.Query().Has("type") {
				t.Errorf("generic search limited to a type: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"results":[{"name":"monolog/monolog","description":"Logging","downloads":5}]}`))
		}
	}))
	defer srv.Close()
	old := composer.PackagistURL
	composer.PackagistURL = srv.URL
	defer func() { composer.PackagistURL = old }()

	n := 0
	if err := (Packagist{}).Index(context.Background(), srv.Client(), func(ps []provider.Package) {
		mu.Lock()
		n += len(ps)
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	if n != indexPages || len(pages) != indexPages {
		t.Errorf("index: %d packages from pages %v", n, pages)
	}
	ps, err := Packagist{}.Search(context.Background(), srv.Client(), "log")
	if err != nil || len(ps) != 1 || ps[0].Provider != "packagist" || ps[0].ID != "monolog/monolog" {
		t.Errorf("search = %+v, %v", ps, err)
	}
}
