package search

import (
	"testing"

	"fzpm/internal/provider"
)

var idx = []provider.Package{
	{Provider: "drupal", ID: "pathauto", Name: "Pathauto", Popularity: 500000},
	{Provider: "drupal", ID: "path_redirect_import", Name: "Path redirect Import", Popularity: 20000},
	{Provider: "drupal", ID: "token", Name: "Token", Popularity: 590000},
	{Provider: "drupal", ID: "token_filter", Name: "Token Filter", Popularity: 40000},
	{Provider: "drupal", ID: "tokenizer_tiny", Name: "Tiny tokenizer", Popularity: 10},
	{Provider: "drupal", ID: "views_bulk_operations", Name: "Views Bulk Operations (VBO)", Popularity: 200000},
}

func TestRank(t *testing.T) {
	cases := map[string]string{
		"pathau": "pathauto",
		"token":  "token",
		"tokn":   "token",
		"vbo":    "views_bulk_operations",
		"":       "token",
	}
	for q, want := range cases {
		got := Rank(q, idx, 0)
		if len(got) == 0 || got[0].ID != want {
			t.Errorf("Rank(%q) first = %v, want %s", q, got, want)
		}
	}
	if got := Rank("zzzz", idx, 0); len(got) != 0 {
		t.Errorf("unexpected matches %v", got)
	}
}

func TestMerge(t *testing.T) {
	out, added := Merge(append([]provider.Package{}, idx[:2]...), []provider.Package{idx[1], idx[2]})
	if added != 1 || len(out) != 3 {
		t.Errorf("added=%d len=%d", added, len(out))
	}
	// A refresh without popularity keeps the known number.
	update := idx[0]
	update.Popularity, update.Summary = 0, "new summary"
	out, _ = Merge(out, []provider.Package{update})
	if out[0].Popularity != idx[0].Popularity || out[0].Summary != "new summary" {
		t.Errorf("merge lost popularity: %+v", out[0])
	}
}
