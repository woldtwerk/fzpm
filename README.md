# fzpm

Fast terminal UI to fuzzy-search and install packages. The core is provider
based; included providers:

[fzpm.webm](https://github.com/user-attachments/assets/fe29ce06-00d2-4bd4-819e-fa90f6b9814f)


| | Drupal | Nuxt |
|---|---|---|
| Project detection | `composer.json` requiring `drupal/core*` (ddev if `.ddev/`) | `package.json` depending on `nuxt` (package manager from the lockfile) |
| Index | top 1000 by installs + newest + recently changed (drupal.org) | all modules on nuxt.com (one request); weekly downloads from [npmx.dev](https://npmx.dev) (nuxt.com's counts are mostly missing) |
| Details | project page + release history | npm registry (README, versions) + nuxt.com (maintainers, category, compatibility, links) |
| `i` / `e` (or `I`) | `composer require` / + `drush en` | `nuxi module add --skipConfig` / `nuxi module add` |
| `d` / `D` | `drush pmu` / + `composer remove` | `nuxi module remove --skipInstall` / + `nuxi module remove --skipConfig` |
| Installed / Uninstalled | `core.extension.yml` / `*.info.yml` + `composer.json` | `modules: [...]` in `nuxt.config` + `./modules` / `package.json` dependencies that are listed on [nuxt.com/modules](https://nuxt.com/modules) (or registered in `nuxt.config`) |

Inside a project the matching provider is enabled automatically; elsewhere
fzpm asks which one to search (single key).

**Multi-select**: `Space` marks a row (`Ctrl-Space` while typing), `Tab` marks
it and moves down a line. Marks are kept while you change the query. `i`
installs, `e` installs and enables (`Alt-i` / `Alt-e` while typing) all marked
rows with one batched command per plugin (e.g.
`composer require drupal/a drupal/b` + `drush en a b -y`, or
`nuxi module add a b`). On *Uninstalled*, they enable all selected modules. `Esc` clears the marks, and so does switching tabs.

**Disable / remove** (normal mode and detail page): `d`
disables, `D` disables and removes the code from the project, for the marked
rows or the row under the cursor. On *Search* a row stands for all of the
package's modules in the project. Removing is refused while another module of
the same package is still enabled, and for core or custom code. Disabled
modules are shown as uninstalled right away (Drupal: `core.extension.yml`
changes after `drush cex`).

**Plugins pane** (`,`; `Alt-,` while typing): `Space` enables/disables a
provider (saved in `~/.config/fzpm/config.json`; disabled providers
are never used), `⏎` uses an enabled provider in the current session only
(e.g. search Nuxt modules from inside a Drupal project). Under *General*,
"Ask which plugin to use when no project is found" can be switched off to search
all enabled plugins right away, and "Nerd Font icons" switched off when your
terminal has no Nerd Font symbols (rows then show the plugin name instead of
its icon). `--provider` overrides the settings.

Plugins can provide an icon (`provider.Iconer`): a Nerd Font glyph and color,
e.g. Drupal `nf-dev-drupal` (U+E742), Nuxt `nf-md-nuxt` (U+F1106).

```
fzpm [flags] [query]

  fzpm pathauto        # start with a query, then alt-I = install + enable
  fzpm -i token        # install without UI
  fzpm -I token        # install + enable without UI
  fzpm --refresh       # rebuild the search index now
  fzpm --images=off    # auto | halfblock | kitty | off  (env FZPM_IMAGES)
  fzpm --provider drupal
```

## Install

Download the archive for your platform from the
[latest release](https://github.com/woldtwerk/fzpm/releases/latest).

Build: `go build -o fzpm ./cmd/fzpm` (Go ≥ 1.22).

## How it works

- **Index**: the 1000 most installed modules, the 500 newest and the 500 most
  recently changed ones are fetched in parallel from drupal.org and cached for
  24 h (`$XDG_CACHE_HOME/fzpm`). Results stream into the list while
  loading; later starts read the cache in ~100 ms.
- **Fuzzy search** runs locally on every keystroke (popularity breaks ties).
  After a short pause the query is also sent to drupal.org full-text search, so
  long-tail modules show up too.
- **Details** (description, images, releases, maintainers, resources,
  development links) are prefetched for all visible rows (3 requests at a time,
  plus images for the highlighted row) and cached on disk. Cached pages always
  open instantly; pages older than 6 h are refreshed in the background, `r`
  reloads on demand. Images are cached permanently and drawn inline at about
  their natural size.
- **Projects**: inside a Drupal project the Drupal provider is enabled
  automatically. Elsewhere, fzpm asks once (single key) whether to
  search anyway; installing then stays disabled.
- **Tabs** (Drupal project only): *Search* (drupal.org; modules already in the
  project are highlighted: green `● enabled`, amber `○ installed` = in the
  codebase but not enabled), *Installed* (enabled modules) and
  *Uninstalled* (in the codebase, not enabled). Read from files only, no
  running site needed: enabled = `core.extension.yml` in the config sync dir;
  present = `*.info.yml` below the docroot plus `drupal/*` modules in
  `composer.json` (versions and package types from `composer.lock`).
  On *Uninstalled*, `i`/`I` enable the module (this does run `drush en`).
  `core.extension.yml` changes only after `drush cex`; modules enabled in the
  current session are shown as installed meanwhile.
- **Commands** (install, enable, disable, remove) run in a pane over the list,
  inside a pseudo terminal: colours, progress bars and prompts work, and
  keys (including `Ctrl-C`) go to the command. The pane closes by itself on
  success and stays open on failure until `Esc`/`⏎`. Where pseudo terminals
  are unavailable (Windows) the command takes over the whole terminal.
- **Install** detects the Drupal project (walks up to a `composer.json`
  requiring `drupal/core*`) and uses `ddev composer` / `ddev drush` when a
  `.ddev` directory exists, otherwise `composer` and `vendor/bin/drush`.
- **Images** (screenshots, maintainer avatars) use the kitty graphics protocol
  when the terminal says it supports it, otherwise Unicode half blocks (work
  everywhere, low resolution). Detection asks the terminal (kitty graphics
  query + DA1), like chafa or yazi do, so it also works through relays: inside
  tmux it needs `set -g allow-passthrough on`; inside nvim's `:terminal` see
  [nvim :terminal](#nvim-terminal).

## nvim :terminal

Neovim's `:terminal` drops graphics escape sequences. With Neovim ≥ 0.12 this
relays kitty graphics (uploads and query answers) to the host terminal, which
lets fzpm and other image-capable tools work inside it (also with
tmux in between):

```lua
local kitty_chan
vim.api.nvim_create_autocmd("TermRequest", {
  desc = "Forward kitty graphics from :terminal to the host terminal",
  callback = function(ev)
    local seq = ev.data.sequence
    if seq:sub(1, 3) == "\27_G" then
      kitty_chan = vim.bo[ev.buf].channel
      vim.api.nvim_ui_send(seq .. ev.data.terminator)
    end
  end,
})
vim.api.nvim_create_autocmd("TermResponse", {
  desc = "Return kitty graphics replies to the :terminal that asked",
  callback = function(ev)
    local seq = ev.data.sequence
    if seq:sub(1, 3) == "\27_G" and kitty_chan then
      vim.api.nvim_chan_send(kitty_chan, seq .. "\27\\")
    end
  end,
})
```

## Keys

| Where | Keys |
|---|---|
| Search (insert, default) | type to filter · `⏎` details · `C-n`/`C-p` (`C-j`/`C-k`) move · `M-i` install · `M-I` install + enable · `M-y` yank · `M-o` browser · `Esc` normal mode |
| List (normal) | `,` plugins · `1`/`2`/`3`, `gt`/`gT` tabs (`M-1`..`M-3` in insert) · `r` refresh (Search: re-fetch the index, like `--refresh`; local tabs: re-read the project; `M-r` in insert) · `j`/`k` · `gg`/`G` · `C-d`/`C-u` · `C-f`/`C-b` · `H`/`L` · `l`/`⏎` details · `i` install · `I` install + enable · `y` yank · `o` browser · `/` search · `S` clear & search · `q` quit · `?` help |
| Details | `j`/`k` · `C-d`/`C-u` · `gg`/`G` · `]r` releases · `[r` top · `J`/`K` scroll sidebar · `n`/`N` select release (install uses its version) · `i`/`I` · `r` reload · `p` toggle images · `y` · `o` · `h`/`Esc`/`q` back |

## Adding a provider

Implement `provider.Provider` (`internal/provider/provider.go`) in
`internal/provider/<name>`, call `provider.Register` in `init()`, and add a blank
import in `cmd/fzpm/main.go`. Caching, search, images and the UI work
unchanged. `Actions()[0]` is bound to `i`, `Actions()[1]` to `I`.

## Third-party licenses

All dependencies use permissive licenses: bubbletea, bubbles, lipgloss,
glamour, sahilm/fuzzy, html-to-markdown, go-osc52, creack/pty, charmbracelet/x/vt (MIT); yaml.v3 (MIT/Apache-2.0); goquery,
golang.org/x/image, golang.org/x/sync (BSD-3-Clause). See `go.mod`.
