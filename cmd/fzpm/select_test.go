package main

import (
	"os"
	"path/filepath"
	"testing"

	"fzpm/internal/provider"
)

func TestSelectProvidersPrefersSpecific(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"drupal site":   {map[string]string{"composer.json": `{"require":{"drupal/core-recommended":"^11"}}`}, "drupal"},
		"typo3 site":    {map[string]string{"composer.json": `{"require":{"typo3/cms-core":"^13"}}`}, "typo3"},
		"php library":   {map[string]string{"composer.json": `{"require":{"php":">=8.2"}}`}, "packagist"},
		"nuxt app":      {map[string]string{"package.json": `{"dependencies":{"nuxt":"^4"}}`}, "nuxt"},
		"node app":      {map[string]string{"package.json": `{"dependencies":{"zod":"^4"}}`}, "npm"},
		"drupal + node": {map[string]string{"composer.json": `{"require":{"drupal/core":"^11"}}`, "package.json": `{}`}, "drupal"},
	}
	for name, tc := range cases {
		dir := t.TempDir()
		for f, content := range tc.files {
			os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644)
		}
		provs, detected, err := selectProviders("", dir, provider.All())
		if err != nil || !detected || len(provs) != 1 || provs[0].Name() != tc.want {
			t.Errorf("%s: got %v (detected=%v, err=%v), want %s", name, names(provs), detected, err, tc.want)
		}
	}
	// Asked for explicitly, a generic provider is used anyway.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"drupal/core":"^11"}}`), 0o644)
	if provs, _, _ := selectProviders("packagist", dir, provider.All()); len(provs) != 1 || provs[0].Name() != "packagist" {
		t.Errorf("--provider packagist: got %v", names(provs))
	}
}
