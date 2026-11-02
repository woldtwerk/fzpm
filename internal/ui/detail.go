package ui

import (
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"fzpm/internal/img"
	"fzpm/internal/provider"
)

const (
	sidebarWidth   = 36
	twoColumnWidth = 110
	maxImageCols   = 120
	maxImageRows   = 40
	avatarCols     = 10
	avatarRows     = 5
)

// rebuildDetail regenerates the detail content. In two-column layouts the
// sidebar lives in its own viewport so it stays put while the main column
// scrolls. It returns commands for kitty image uploads not sent yet.
func (m *Model) rebuildDetail(resetScroll bool) tea.Cmd {
	h := max(1, m.height-1)
	twoCol := m.width >= twoColumnWidth
	leftW := m.width - 2
	if twoCol {
		leftW = m.width - sidebarWidth - 4
	}

	var cmds []tea.Cmd
	left := m.detailMain(leftW, &cmds)
	m.releasesLine = 0
	for i, l := range left {
		if strings.Contains(l, "\x00releases") {
			m.releasesLine = i
			left[i] = strings.ReplaceAll(l, "\x00releases", "")
		}
	}
	for i := range left {
		left[i] = " " + ansi.Truncate(left[i], leftW, "")
	}
	side := m.detailSidebar(min(sidebarWidth, m.width-2), &cmds)

	if twoCol {
		m.sideW = sidebarWidth
		m.vp.Width, m.vp.Height = leftW+1, h
		m.side.Width, m.side.Height = sidebarWidth, h
		m.vp.SetContent(strings.Join(left, "\n"))
		m.side.SetContent(strings.Join(side, "\n"))
	} else {
		m.sideW = 0
		m.vp.Width, m.vp.Height = m.width, h
		for i := range side {
			side[i] = " " + side[i]
		}
		m.vp.SetContent(strings.Join(left, "\n") + "\n\n" + strings.Join(side, "\n"))
	}
	if resetScroll {
		m.vp.GotoTop()
		m.side.GotoTop()
	}
	return tea.Batch(cmds...)
}

func (m *Model) detailView() string {
	if m.sideW == 0 {
		return m.vp.View()
	}
	sep := strings.TrimSuffix(strings.Repeat(dim.Render(" │ ")+"\n", m.vp.Height), "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, m.vp.View(), sep, m.side.View())
}

func (m *Model) detailMain(w int, cmds *[]tea.Cmd) []string {
	p := m.detailFor
	var out []string
	add := func(s string) { out = append(out, strings.Split(s, "\n")...) }

	name := p.Name
	if m.detail != nil && m.detail.Name != "" {
		name = m.detail.Name
	}
	add("")
	add(m.providerTag(p.Provider) + bold.Inherit(accent).Render(name) + "  " + dim.Render(p.ID))

	pop := p.Popularity
	if m.detail != nil && m.detail.Popularity > 0 {
		pop = m.detail.Popularity
	}
	label := "installs"
	if m.detail != nil && m.detail.PopularityLabel != "" {
		label = m.detail.PopularityLabel
	}
	meta := []string{accent.Render("★ " + humanInt(pop) + " " + label)}
	if !p.Updated.IsZero() {
		meta = append(meta, "updated "+p.Updated.Format("2006-01-02"))
	}
	if m.detail != nil {
		meta = append(meta, link(m.detail.URL, dim.Render(m.detail.URL)))
	}
	add(strings.Join(meta, dim.Render("  ·  ")))
	add("")

	switch {
	case m.detailErr != nil:
		add(errStyle.Render("Could not load details: " + m.detailErr.Error()))
		add(dim.Render(wrap(p.Summary, w)))
		return out
	case m.detail == nil:
		add(dim.Render(wrap(p.Summary, w)))
		add("")
		add(m.spinner.View() + dim.Render(" loading details…"))
		return out
	}

	// The body carries inline image markers; draw the images in place.
	for _, line := range strings.Split(m.renderBody(w), "\n") {
		if u, ok := strings.CutPrefix(line, imageMarker); ok {
			add(m.inlineImage(u, w, cmds))
			continue
		}
		add(line)
	}

	// Gallery images that the description does not already show.
	inline := map[string]bool{}
	for _, u := range bodyImages(m.detail.Body) {
		inline[u] = true
	}
	var gallery []string
	for _, u := range m.detail.Images {
		if !inline[u] {
			gallery = append(gallery, u)
		}
	}
	if m.showImages && len(gallery) > 0 {
		add(heading("Images", w))
		for _, u := range gallery {
			add(m.inlineImage(u, w, cmds))
			add("")
		}
	}

	if len(m.detail.Releases) > 0 {
		add(heading("Releases", w) + "\x00releases")
		hdr := "  " + pad("Version", 16) + " " + pad("Released", 10) + "  " + pad("Compatibility", 26) + " Notes"
		add(bold.Render(hdr))
		add(dim.Render("  " + strings.Repeat("─", max(0, min(w, 100)-2))))
		for i, r := range m.detail.Releases {
			marker := "  "
			if i == m.relCursor {
				marker = accent.Render("▸ ")
			}
			line := marker + link(r.URL, bold.Render(pad(r.Version, 16))) + " " +
				dim.Render(pad(r.Date.Format("2006-01-02"), 10)) + "  " +
				pad(r.Compat, 26) + " " + dim.Render(strings.Join(r.Tags, ", "))
			if i == m.relCursor {
				line = selStyle.Render(line)
			}
			add(line)
		}
	}
	add("")
	return out
}

// inlineImage renders one image at its natural size (fitted to w), or a
// one-line stand-in while it loads or when images are off.
func (m *Model) inlineImage(u string, w int, cmds *[]tea.Cmd) string {
	if !m.showImages {
		return "  " + dim.Render("🖼 "+u)
	}
	im, ok := m.images[u]
	if !ok {
		if m.imageFailed[u] {
			return "" // e.g. SVG or unreachable; the caption/alt text stays
		}
		return "  " + dim.Render("loading "+u)
	}
	cols, rows := img.Fit(im, min(w-2, maxImageCols), m.maxRows(maxImageRows), m.opts.ImageMode)
	if cols == 0 {
		return ""
	}
	return indent(m.renderImage(u, im, cols, rows, cmds), 2)
}

// releaseRow maps a release index to its line in the main column.
func (m *Model) releaseRow(i int) int { return m.releasesLine + 3 + i }

func (m *Model) maxRows(rows int) int {
	if m.opts.ImageMode == img.Kitty {
		return min(rows, img.MaxKittyRows)
	}
	return rows
}

// renderImage draws an image with the active mode, queueing a kitty upload
// the first time a given image/size is shown.
func (m *Model) renderImage(url string, im image.Image, cols, rows int, cmds *[]tea.Cmd) string {
	if m.opts.ImageMode == img.Kitty {
		id := img.ID(fmt.Sprintf("%s#%dx%d", url, cols, rows))
		if !m.kittySent[id] {
			if m.paneVisible() {
				m.kittySent[id] = true
				*cmds = append(*cmds, writeTerminal(img.KittyTransmit(im, id, cols, rows, insideTmux())))
			} else {
				m.kittyPending = true // tmux would drop it; retry when visible
			}
		}
		return img.KittyPlaceholder(id, cols, rows)
	}
	bg := color.Color(color.Black)
	if !m.opts.Dark {
		bg = color.White
	}
	return img.HalfBlocks(im, cols, rows, bg)
}

func (m *Model) renderBody(w int) string {
	if m.renderedBody != "" && m.renderedWidth == w {
		return m.renderedBody
	}
	if m.md == nil || m.mdWidth != w {
		style := "dark"
		if !m.opts.Dark {
			style = "light"
		}
		r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(w-2))
		if err != nil {
			return wrap(m.detail.Body, w)
		}
		m.md, m.mdWidth = r, w
	}
	// Render text between images with glamour; images become marker lines
	// that detailMain replaces with the actual picture.
	var parts []string
	render := func(md string) {
		if strings.TrimSpace(md) == "" {
			return
		}
		out, err := m.md.Render(md)
		if err != nil {
			out = wrap(md, w)
		}
		parts = append(parts, strings.Trim(out, "\n"))
	}
	body, prev := m.detail.Body, 0
	for _, loc := range imageRe.FindAllStringSubmatchIndex(body, -1) {
		render(body[prev:loc[0]])
		sub := func(i int) string {
			if loc[2*i] < 0 {
				return ""
			}
			return body[loc[2*i]:loc[2*i+1]]
		}
		src, caption := sub(2), strings.TrimSpace(sub(3))
		if src == "" {
			src = sub(5)
		}
		parts = append(parts, "", imageMarker+src)
		if caption != "" {
			render(caption)
		}
		parts = append(parts, "")
		prev = loc[1]
	}
	render(body[prev:])
	out := strings.Join(parts, "\n")
	m.renderedBody, m.renderedWidth = out, w
	return out
}

const imageMarker = "\x00img:"

// imageRe matches a linked image with optional caption,
// [![alt](src) caption](href), or a plain image ![alt](src "title").
var imageRe = regexp.MustCompile(`\[!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)([^\[\]]*)\]\([^)\s]*(?:\s+"[^"]*")?\)|!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// bodyImages lists the image URLs referenced in markdown.
func bodyImages(md string) []string {
	var out []string
	for _, m := range imageRe.FindAllStringSubmatch(md, -1) {
		if m[2] != "" {
			out = append(out, m[2])
		} else {
			out = append(out, m[5])
		}
	}
	return out
}

func (m *Model) detailSidebar(w int, cmds *[]tea.Cmd) []string {
	out := []string{""}
	if m.detail == nil {
		return out
	}
	for _, s := range m.detail.Sidebar {
		if len(s.Items) == 0 {
			continue
		}
		out = append(out, bold.Inherit(accent).Render(s.Title), dim.Render(strings.Repeat("─", w)))
		if hasImages(s) && m.showImages {
			out = append(out, m.avatarGrid(s.Items, w, cmds)...)
			out = append(out, "")
			continue
		}
		for _, it := range s.Items {
			if it.URL == "" {
				for i, l := range strings.Split(wrap(it.Label, w-2), "\n") {
					if i == 0 {
						out = append(out, "• "+l)
					} else {
						out = append(out, "  "+l)
					}
				}
				continue
			}
			out = append(out, "• "+link(it.URL, ansi.Truncate(it.Label, w-2, "…")))
		}
		out = append(out, "")
	}
	return out
}

func hasImages(s provider.Section) bool {
	for _, it := range s.Items {
		if it.Image != "" {
			return true
		}
	}
	return false
}

var avatarFallback = lipgloss.NewStyle().
	Width(avatarCols).Height(avatarRows).
	Align(lipgloss.Center, lipgloss.Center).
	Background(lipgloss.AdaptiveColor{Light: "#d0d7de", Dark: "#30363d"})

// avatarGrid renders items as avatar tiles with the name underneath.
func (m *Model) avatarGrid(items []provider.Link, w int, cmds *[]tea.Cmd) []string {
	cell := avatarCols + 2
	perRow := max(1, (w+2)/cell)
	var out []string
	for start := 0; start < len(items); start += perRow {
		row := items[start:min(start+perRow, len(items))]
		tiles := make([]string, 0, len(row)*2)
		for i, it := range row {
			tile := avatarFallback.Render(initials(it.Label))
			if im, ok := m.images[it.Image]; ok {
				cols, rows := img.Fit(im, avatarCols, m.maxRows(avatarRows), m.opts.ImageMode)
				tile = m.renderImage(it.Image, im, cols, rows, cmds)
			}
			name := link(it.URL, ansi.Truncate(it.Label, avatarCols, "…"))
			tile = lipgloss.NewStyle().Width(avatarCols).Render(tile + "\n" + name)
			if i > 0 {
				tiles = append(tiles, "  ")
			}
			tiles = append(tiles, tile)
		}
		out = append(out, strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, tiles...), "\n")...)
		out = append(out, "")
	}
	return out
}

func initials(name string) string {
	var r []rune
	for _, f := range strings.Fields(name) {
		r = append(r, []rune(strings.ToUpper(f))[0])
		if len(r) == 2 {
			break
		}
	}
	return string(r)
}

// sidebarImages lists avatar URLs of the current detail.
func (m *Model) sidebarImages() []string {
	var out []string
	if m.detail == nil {
		return out
	}
	for _, s := range m.detail.Sidebar {
		for _, it := range s.Items {
			if it.Image != "" {
				out = append(out, it.Image)
			}
		}
	}
	return out
}

func heading(title string, w int) string {
	rule := max(0, min(w, 80)-lipgloss.Width(title)-1)
	return "\n" + bold.Inherit(accent).Render(title) + " " + dim.Render(strings.Repeat("─", rule))
}

// link wraps text in an OSC 8 hyperlink (clickable in most terminals).
func link(url, text string) string {
	if url == "" {
		return text
	}
	return ansi.SetHyperlink(url) + text + ansi.ResetHyperlink()
}

func wrap(s string, w int) string {
	return lipgloss.NewStyle().Width(max(10, w)).Render(s)
}

func indent(s string, n int) string {
	p := strings.Repeat(" ", n)
	return p + strings.ReplaceAll(s, "\n", "\n"+p)
}
