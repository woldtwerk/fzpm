// Package search ranks packages for a query with fuzzy matching.
package search

import (
	"math"
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"

	"fzpm/internal/provider"
)

type source []provider.Package

func (s source) String(i int) string { return s[i].Name + " " + s[i].ID }
func (s source) Len() int            { return len(s) }

// Rank returns the packages matching query, best first. An empty query
// returns all packages ordered by popularity. limit <= 0 means no limit.
func Rank(query string, pkgs []provider.Package, limit int) []provider.Package {
	q := strings.ToLower(strings.Join(strings.Fields(query), ""))
	var out []provider.Package
	if q == "" {
		out = append(out, pkgs...)
		sort.SliceStable(out, func(i, j int) bool { return out[i].Popularity > out[j].Popularity })
	} else {
		matches := fuzzy.FindFromNoSort(q, source(pkgs))
		scores := make([]float64, len(matches))
		for i, m := range matches {
			scores[i] = score(q, m.Score, pkgs[m.Index])
		}
		idx := make([]int, len(matches))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
		out = make([]provider.Package, len(idx))
		for i, k := range idx {
			out[i] = pkgs[matches[k].Index]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func score(q string, fuzzyScore int, p provider.Package) float64 {
	s := float64(fuzzyScore) + 4*math.Log10(float64(p.Popularity)+1)
	id, name := strings.ToLower(p.ID), strings.ToLower(strings.ReplaceAll(p.Name, " ", ""))
	switch {
	case id == q || name == q:
		s += 100
	case strings.HasPrefix(id, q) || strings.HasPrefix(name, q):
		s += 30
	case strings.Contains(id, q) || strings.Contains(name, q):
		s += 15
	}
	return s
}

// Merge adds pkgs to index (deduplicated by provider+ID, newer data wins,
// except that a missing popularity (0) keeps the known one) and reports how
// many were new.
func Merge(index []provider.Package, pkgs []provider.Package) ([]provider.Package, int) {
	pos := make(map[string]int, len(index))
	for i, p := range index {
		pos[p.Provider+"/"+p.ID] = i
	}
	added := 0
	for _, p := range pkgs {
		k := p.Provider + "/" + p.ID
		if i, ok := pos[k]; ok {
			if p.Popularity == 0 {
				p.Popularity = index[i].Popularity
			}
			index[i] = p
			continue
		}
		pos[k] = len(index)
		index = append(index, p)
		added++
	}
	return index, added
}
