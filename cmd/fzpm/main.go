// Command fzpm searches and installs packages (Drupal modules first)
// from the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"fzpm/internal/app"
	"fzpm/internal/cache"
	"fzpm/internal/config"
	"fzpm/internal/img"
	"fzpm/internal/provider"
	"fzpm/internal/runner"
	"fzpm/internal/ui"

	// Providers register themselves; add new sources here.
	_ "fzpm/internal/provider/drupal"
	_ "fzpm/internal/provider/nuxt"
)

// version is set by GoReleaser; local builds are "dev" and never update
// themselves.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fzpm:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		refresh   = flag.Bool("refresh", false, "rebuild the search index now")
		provFlag  = flag.String("provider", "", "comma separated providers (default: detected from the current project, else all)")
		imageFlag = flag.String("images", os.Getenv("FZPM_IMAGES"), "image rendering: auto|halfblock|kitty|off (env FZPM_IMAGES)")
		install   = flag.String("i", "", "install `package` without the UI")
		enable    = flag.String("I", "", "install and enable `package` without the UI")
		showVer   = flag.Bool("version", false, "print version")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: fzpm [flags] [query]\n\nproviders: %s\n\n", names(provider.All()))
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println("fzpm", version)
		return nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fzpm: config:", err)
	}
	provs, detected, err := selectProviders(*provFlag, dir, enabledProviders(cfg))
	if err != nil {
		return err
	}
	direct := *install != "" || *enable != ""
	if !detected && *provFlag == "" && !direct && !cfg.SkipPrompt {
		// No project here: searching is still possible, but only on purpose.
		provs, err = askProviders(provs, dir, readKey, os.Stdout)
		if errors.Is(err, errCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
	}

	switch {
	case *install != "":
		return installDirect(provs, dir, *install, 0)
	case *enable != "":
		return installDirect(provs, dir, *enable, 1)
	}

	mode, err := img.ParseMode(*imageFlag)
	if err != nil {
		return err
	}
	store, err := cache.Open("fzpm")
	if err != nil {
		return err
	}
	svc := app.New(store, version)
	m := ui.New(svc, provs, ui.Options{
		Query:     strings.Join(flag.Args(), " "),
		Refresh:   *refresh,
		ImageMode: mode,
		Dark:      lipgloss.HasDarkBackground(),
		Dir:       dir,
		Config:    cfg,
		Version:   version,
	})
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithReportFocus()).Run()
	return err
}

// enabledProviders are the registered providers not switched off in the
// settings (all of them if everything is off).
func enabledProviders(cfg *config.Config) []provider.Provider {
	var out []provider.Provider
	for _, p := range provider.All() {
		if cfg.Enabled(p.Name()) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return provider.All()
	}
	return out
}

// selectProviders returns the providers named by --provider (regardless of
// the settings), else the enabled ones that detect a project in dir
// (detected = true), else all enabled ones.
func selectProviders(flagVal, dir string, enabled []provider.Provider) (provs []provider.Provider, detected bool, err error) {
	if flagVal != "" {
		for _, n := range strings.Split(flagVal, ",") {
			p, ok := provider.Get(strings.TrimSpace(n))
			if !ok {
				return nil, false, fmt.Errorf("unknown provider %q (available: %s)", n, names(provider.All()))
			}
			provs = append(provs, p)
		}
		return provs, false, nil
	}
	for _, p := range enabled {
		if _, ok := p.Detect(dir); ok {
			provs = append(provs, p)
		}
	}
	if len(provs) > 0 {
		return provs, true, nil
	}
	return enabled, false, nil
}

// installDirect runs an action for an exact package name without the UI.
func installDirect(provs []provider.Provider, dir, id string, action int) error {
	for _, p := range provs {
		proj, ok := p.Detect(dir)
		if !ok {
			continue
		}
		actions := p.Actions()
		if action >= len(actions) {
			return fmt.Errorf("%s has no action %d", p.Name(), action+1)
		}
		cmds, err := actions[action].Commands([]provider.Package{{Provider: p.Name(), ID: id}}, "", proj)
		if err != nil {
			return err
		}
		seq := &runner.Seq{Cmds: cmds, NoPause: true}
		seq.SetStdin(os.Stdin)
		seq.SetStdout(os.Stdout)
		seq.SetStderr(os.Stderr)
		return seq.Run()
	}
	return errors.New("no supported project found in the current directory or its parents")
}

func names(provs []provider.Provider) string {
	return strings.Join(nameList(provs), ", ")
}
