// Package readme fetches and cleans up project READMEs (GitHub flavoured
// markdown) for the terminal detail page. Shared by providers whose detail
// body is a README.
package readme

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"fzpm/internal/provider"
)

var ghRepoRe = regexp.MustCompile(`github\.com[/:]([^/]+/[^/#.]+)`)

// GitHubRepo extracts "owner/repo" from a repository URL.
func GitHubRepo(u string) string {
	if m := ghRepoRe.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// RawBase returns the raw.githubusercontent.com base for a repository
// reference ("owner/repo", "owner/repo#branch", "owner/repo/sub/dir").
func RawBase(repo string) string {
	if repo == "" {
		return ""
	}
	path, branch, _ := strings.Cut(repo, "#")
	if branch == "" {
		branch = "HEAD"
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) < 2 {
		return ""
	}
	base := "https://raw.githubusercontent.com/" + segs[0] + "/" + segs[1] + "/" + branch + "/"
	if len(segs) > 2 {
		base += strings.Join(segs[2:], "/") + "/"
	}
	return base
}

// Fetch returns README.md below base (a raw file URL prefix ending in "/"),
// or "" when there is none.
func Fetch(ctx context.Context, c *http.Client, base string) string {
	if base == "" {
		return ""
	}
	for _, name := range []string{"README.md", "readme.md", "Readme.md"} {
		r, err := provider.Fetch(ctx, c, base+name)
		if err != nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(r, 1<<20))
		r.Close()
		if err == nil {
			return string(b)
		}
	}
	return ""
}

var (
	htmlImgRe   = regexp.MustCompile(`(?i)<img\s[^>]*?src=["']([^"']+)["'][^>]*>`)
	htmlAltRe   = regexp.MustCompile(`(?i)\salt=["']([^"']*)["']`)
	htmlTagRe   = regexp.MustCompile(`(?i)</?(p|div|a|br|picture|source|center|span|h[1-6]|sup|sub|details|summary)(\s[^>]*)?/?>`)
	linkedImgRe = regexp.MustCompile(`\[!\[[^\]]*\]\(([^)\s]+)[^)]*\)\]\([^)]*\)`)
	mdImgRe     = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)([^)]*)\)`)
	ghBlobRe    = regexp.MustCompile(`^https://github\.com/([^/]+/[^/]+)/blob/(.+)$`)
)

// Clean makes a README render well in the terminal: HTML images
// become markdown images, badges go, relative image paths become absolute,
// and layout-only HTML tags are dropped. Code blocks are left alone.
func Clean(md, base string) string {
	md = inlineReferences(md)
	lines := strings.Split(md, "\n")
	inCode := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		lines[i] = outsideInlineCode(l, func(l string) string { return cleanLine(l, base) })
	}
	return strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// outsideInlineCode applies fn to the parts of a line that are not inside
// `inline code`.
func outsideInlineCode(l string, fn func(string) string) string {
	parts := strings.Split(l, "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = fn(parts[i])
	}
	return strings.Join(parts, "`")
}

func cleanLine(l, base string) string {
	l = htmlImgRe.ReplaceAllStringFunc(l, func(tag string) string {
		src := htmlImgRe.FindStringSubmatch(tag)[1]
		alt := ""
		if m := htmlAltRe.FindStringSubmatch(tag); m != nil {
			alt = m[1]
		}
		return "![" + alt + "](" + src + ")"
	})
	l = htmlTagRe.ReplaceAllString(l, "")
	l = linkedImgRe.ReplaceAllStringFunc(l, func(s string) string {
		if isBadge(linkedImgRe.FindStringSubmatch(s)[1]) {
			return ""
		}
		return s
	})
	l = mdImgRe.ReplaceAllStringFunc(l, func(s string) string {
		m := mdImgRe.FindStringSubmatch(s)
		if isBadge(m[2]) {
			return ""
		}
		return "![" + m[1] + "](" + absImage(m[2], base) + ")"
	})
	return l
}

var (
	refDefRe       = regexp.MustCompile(`(?m)^ {0,3}\[([^\]]+)\]:[ \t]*<?(\S+?)>?(?:[ \t]+["'(].*)?[ \t]*$\n?`)
	refImageRe     = regexp.MustCompile(`!\[([^\]]*)\]\[([^\]]*)\]`)
	refLinkedImgRe = regexp.MustCompile(`\[(!\[[^\]]*\]\([^)]*\))\]\[([^\]]*)\]`)
	refLinkRe      = regexp.MustCompile(`\[([^\]]+)\]\[([^\]]*)\]`)
)

// inlineReferences turns reference-style links and images ([x][ref] with
// "[ref]: url" definitions, popular for README badges) into inline ones.
func inlineReferences(md string) string {
	defs := map[string]string{}
	for _, m := range refDefRe.FindAllStringSubmatch(md, -1) {
		defs[strings.ToLower(m[1])] = m[2]
	}
	if len(defs) == 0 {
		return md
	}
	md = refDefRe.ReplaceAllString(md, "")
	lookup := func(label, ref string) (string, bool) {
		if ref == "" {
			ref = label
		}
		u, ok := defs[strings.ToLower(ref)]
		return u, ok
	}
	md = refImageRe.ReplaceAllStringFunc(md, func(s string) string {
		m := refImageRe.FindStringSubmatch(s)
		if u, ok := lookup(m[1], m[2]); ok {
			return "![" + m[1] + "](" + u + ")"
		}
		return s
	})
	for _, re := range []*regexp.Regexp{refLinkedImgRe, refLinkRe} {
		md = re.ReplaceAllStringFunc(md, func(s string) string {
			m := re.FindStringSubmatch(s)
			if u, ok := lookup(m[1], m[2]); ok {
				return "[" + m[1] + "](" + u + ")"
			}
			return s
		})
	}
	return md
}

func isBadge(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "shields.io") || strings.Contains(l, "badgen.net") || strings.Contains(l, "poser.pugx.org") ||
		strings.Contains(l, "badge") || strings.Contains(l, "codecov.io") ||
		strings.HasSuffix(strings.SplitN(l, "?", 2)[0], ".svg")
}

func absImage(u, base string) string {
	if m := ghBlobRe.FindStringSubmatch(u); m != nil {
		return "https://raw.githubusercontent.com/" + m[1] + "/" + m[2]
	}
	if strings.Contains(u, "://") || base == "" || strings.HasPrefix(u, "data:") {
		return u
	}
	b, err := url.Parse(base)
	if err != nil {
		return u
	}
	ref, err := url.Parse(strings.TrimPrefix(u, "/"))
	if err != nil {
		return u
	}
	return b.ResolveReference(ref).String()
}

func Images(md string) []string {
	var out []string
	for _, m := range mdImgRe.FindAllStringSubmatch(md, -1) {
		out = append(out, m[2])
	}
	return out
}
