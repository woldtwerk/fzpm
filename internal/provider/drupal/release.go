package drupal

import (
	"encoding/xml"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fzpm/internal/provider"
)

type releaseHistory struct {
	Terms    []term `xml:"terms>term"`
	Releases []struct {
		Version  string `xml:"version"`
		Status   string `xml:"status"`
		Link     string `xml:"release_link"`
		Date     int64  `xml:"date"`
		Terms    []term `xml:"terms>term"`
		Security struct {
			Covered string `xml:"covered,attr"`
		} `xml:"security"`
		Compat string `xml:"core_compatibility"`
	} `xml:"releases>release"`
}

type term struct {
	Name  string `xml:"name"`
	Value string `xml:"value"`
}

// parseReleases parses updates.drupal.org release history. It also returns
// project-level status facts (maintenance / development status).
func parseReleases(r io.Reader) ([]provider.Release, []provider.Link, error) {
	var h releaseHistory
	if err := xml.NewDecoder(r).Decode(&h); err != nil {
		return nil, nil, err
	}
	var status []provider.Link
	for _, t := range h.Terms {
		if t.Name == "Maintenance status" || t.Name == "Development status" {
			status = append(status, provider.Link{Label: t.Value})
		}
	}
	out := make([]provider.Release, 0, len(h.Releases))
	for _, x := range h.Releases {
		if x.Status != "" && x.Status != "published" {
			continue
		}
		rel := provider.Release{
			Version:    x.Version,
			Constraint: Constraint(x.Version),
			Date:       time.Unix(x.Date, 0),
			Compat:     x.Compat,
			URL:        x.Link,
		}
		if x.Security.Covered == "1" {
			rel.Tags = append(rel.Tags, "security covered")
		}
		for _, t := range x.Terms {
			if t.Name == "Release type" {
				rel.Tags = append(rel.Tags, t.Value)
			}
		}
		out = append(out, rel)
	}
	return out, status, nil
}

var (
	legacyRe = regexp.MustCompile(`^\d+\.x-(\d.*)$`)
	stableRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.\d+)?$`)
)

// Constraint converts a drupal.org version into a composer constraint,
// mirroring what drupal.org suggests:
//
//	8.x-1.18     -> ^1.18
//	2.0.3        -> ^2.0
//	8.x-1.x-dev  -> 1.x-dev
//	3.0.0-beta1  -> 3.0.0-beta1
func Constraint(v string) string {
	if m := legacyRe.FindStringSubmatch(v); m != nil {
		v = m[1]
	}
	if strings.HasSuffix(v, "-dev") {
		return v
	}
	if m := stableRe.FindStringSubmatch(v); m != nil {
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		return "^" + strconv.Itoa(major) + "." + strconv.Itoa(minor)
	}
	return v
}
