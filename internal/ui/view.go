package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	accentColor    = lipgloss.AdaptiveColor{Light: "#0678be", Dark: "#53b0eb"}
	accent         = lipgloss.NewStyle().Foreground(accentColor)
	bold           = lipgloss.NewStyle().Bold(true)
	dim            = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#777", Dark: "#888"})
	errStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#e05555"))
	okStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950"))
	selStyle       = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#dde9f5", Dark: "#1f3447"})
	modeInsert     = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("#000")).Background(lipgloss.Color("#3fb950"))
	modeNormal     = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("#000")).Background(lipgloss.Color("#53b0eb"))
	modeDetail     = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("#000")).Background(lipgloss.Color("#d29922"))
	badge          = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#bc8cff"})
	enabledStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"})
	installedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"})
	tabActive      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000")).Background(accentColor)
	tabInactive    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#555", Dark: "#aaa"})
)

func (m *Model) View() string {
	if m.term != nil {
		return overlay(m.baseView(), m.termView(), m.width, m.height)
	}
	return m.baseView()
}

func (m *Model) baseView() string {
	if m.settingsOpen {
		return m.settingsView()
	}
	if m.help {
		return m.helpView()
	}
	if m.screen == screenDetail {
		return m.detailView() + "\n" + m.footer()
	}
	return m.listView()
}

func (m *Model) listView() string {
	var b strings.Builder
	if m.hasTabs() {
		b.WriteString(ansi.Truncate(m.tabBar(), m.width, "") + "\n")
	}
	left := ansi.Truncate(m.input.View(), m.input.Width+3, "")
	right := m.projectLabel()
	if avail := m.width - lipgloss.Width(left) - 1; lipgloss.Width(right) > avail {
		right = "…" + ansi.TruncateLeft(right, lipgloss.Width(right)-avail+1, "")
	}
	gap := max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	b.WriteString(ansi.Truncate(left, m.width, "") + strings.Repeat(" ", gap) + right + "\n")
	b.WriteString(dim.Render(strings.Repeat("─", m.width)) + "\n")

	h := m.listHeight()
	multi := len(m.provs) > 1
	nameW := min(32, max(12, m.width/4))
	idW := min(28, max(10, m.width/5))
	for i := m.offset; i < m.offset+h; i++ {
		if i >= len(m.results) {
			if i == 0 {
				msg := "no matches"
				if m.indexing > 0 || m.searching > 0 || m.localLoading > 0 {
					msg = "loading…"
				}
				b.WriteString("  " + dim.Render(msg))
			}
			b.WriteString("\n")
			continue
		}
		p := m.results[i]
		marker := "  "
		if i == m.cursor {
			marker = accent.Render("▌ ")
		}
		if _, ok := m.marked[key(p)]; ok {
			marker += okStyle.Render("✓") + " "
		} else {
			marker += "  "
		}
		line := marker
		if multi {
			line += m.providerTag(p.Provider)
		}
		if lp, ok := m.localByKey[key(p)]; ok && m.tab != tabSearch {
			line += bold.Render(pad(p.Name, nameW)) + " " + dim.Render(pad(p.ID, idW)) + " " +
				badge.Render(pad(lp.Origin, 10)) + " " + accent.Render(pad(lp.Version, 14)) + " " + dim.Render(lp.Group)
		} else {
			// Highlight packages that are already in the local project.
			name, tag := bold, strings.Repeat(" ", 12)
			if enabled, ok := m.present[key(p)]; ok {
				name, tag = installedStyle, installedStyle.Render(pad("○ installed", 12))
				if enabled {
					name, tag = enabledStyle, enabledStyle.Render(pad("● enabled", 12))
				}
			}
			line += name.Render(pad(p.Name, nameW)) + " " + dim.Render(pad(p.ID, idW)) + " " +
				accent.Render(fmt.Sprintf("%7s", humanInt(p.Popularity))) + "  " + tag + p.Summary
		}
		line = ansi.Truncate(line, m.width, "…")
		if i == m.cursor {
			line = selStyle.Width(m.width).Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String() + m.footer()
}

func (m *Model) projectLabel() string {
	var parts []string
	for _, p := range m.provs {
		if proj, ok := m.projects[p.Name()]; ok {
			parts = append(parts, proj.Label)
		}
	}
	if len(parts) == 0 {
		return dim.Render("no project detected (install disabled) ")
	}
	return okStyle.Render(strings.Join(parts, " · ") + " ")
}

func (m *Model) footer() string {
	var mode string
	switch {
	case m.screen == screenDetail:
		mode = modeDetail.Render("DETAIL")
	case m.insert:
		mode = modeInsert.Render("INSERT")
	default:
		mode = modeNormal.Render("NORMAL")
	}
	parts := []string{mode}
	if m.busy() {
		what := "indexing"
		switch {
		case m.screen == screenDetail && m.detail == nil:
			what = "loading"
		case m.screen == screenDetail && m.refreshing:
			what = "refreshing"
		case m.searching > 0:
			what = "searching"
		case m.localLoading > 0:
			what = "reading project"
		}
		parts = append(parts, m.spinner.View()+" "+dim.Render(what))
	}
	if m.screen == screenList {
		parts = append(parts, dim.Render(fmt.Sprintf("%d/%d", min(len(m.results), m.cursor+1), len(m.results))))
		if n := len(m.markedRows()); n > 0 {
			parts = append(parts, okStyle.Render(fmt.Sprintf("✓ %d selected", n)))
		}
	} else if v := m.selectedConstraint(); v != "" {
		parts = append(parts, accent.Render("version "+v))
	}
	if m.status != "" {
		st := okStyle
		if m.statusErr {
			st = errStyle
		}
		parts = append(parts, st.Render(m.status))
	}
	line := strings.Join(parts, " ")
	hint := dim.Render(m.hint())
	if gap := m.width - lipgloss.Width(line) - lipgloss.Width(hint); gap > 0 {
		line += strings.Repeat(" ", gap) + hint
	}
	return ansi.Truncate(line, m.width, "…")
}

func (m *Model) hint() string {
	switch {
	case m.screen == screenDetail:
		return "i install  e +enable  d/D disable/remove  n/N version  ]r releases  r reload  h back  ? help"
	case m.insert:
		return "⏎ details  tab mark  M-i install  M-e +enable  esc normal  ? (normal) help"
	default:
		return "i install  e +enable  d/D disable/remove  ␣/tab mark  l details  / search  r refresh  , plugins  ? help"
	}
}

const helpText = `fzpm — keys

 SEARCH (insert mode, default)
   type             fuzzy filter (remote search kicks in after a pause)
   ⏎                open details            ctrl-n/p, ctrl-j/k  move
   alt-i            install                 alt-e, alt-I        install + enable
   alt-y            yank install command    alt-o               open in browser
   tab              mark row + move down    ctrl-space          mark row
   esc              clear marks, else normal mode

 LIST (normal mode)
   j/k              down/up                 gg/G                top/bottom
   ctrl-d/u         half page               ctrl-f/b            page
   H/L              top/bottom of screen    l, ⏎                details
   i                install                 e, I                install + enable
   space            mark row                tab                 mark row + move down
   d                disable                 D                   disable + remove
                    (i / e / d / D act on all marked rows)
   esc              clear marks
   y                yank command            o                   open in browser
   /, a             search (insert)         S                   clear & search
   1/2/3, gt/gT     tabs: search / installed / uninstalled (alt-1..3 in insert)
   r                refresh list: re-fetch index (search) / re-read project
                    (installed/uninstalled tabs); alt-r in insert
   ,                plugins (enable/disable; alt-, in insert)
   q                quit                    ?                   help

 DETAILS
   j/k, ctrl-e/y    scroll line             ctrl-d/u, ctrl-f/b  half / full page
   gg/G             top/bottom              ]r / [r             releases / top
   J/K              scroll sidebar
   n/N              select next/prev release (install uses its version)
   i / e            install / + enable      p                   toggle images
   d / D            disable / + remove
   r                reload from source      y, o                yank, open
   h, esc, q        back

 COMMANDS run in a pane: keys go to the command (ctrl-c aborts);
   it closes on success, esc / ⏎ close it after a failure

 press any key to close`

func (m *Model) helpView() string {
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentColor).Padding(0, 2)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box.Render(helpText))
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if n := lipgloss.Width(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

func humanInt(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}
