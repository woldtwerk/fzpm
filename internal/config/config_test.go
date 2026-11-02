package config

import (
	"path/filepath"
	"testing"
)

func TestConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	c, err := LoadFile(path)
	if err != nil || !c.Enabled("drupal") {
		t.Fatalf("defaults: %v %v", err, c.Disabled)
	}
	c.SetEnabled("nuxt", false)
	c.SetEnabled("drupal", false)
	c.SetEnabled("drupal", true)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadFile(path)
	if err != nil || c2.Enabled("nuxt") || !c2.Enabled("drupal") || len(c2.Disabled) != 1 {
		t.Errorf("reloaded: %v %+v", err, c2.Disabled)
	}
}
