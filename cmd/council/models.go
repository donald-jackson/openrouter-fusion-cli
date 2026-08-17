package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/config"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/render"
)

// catalogTimeout bounds discovery. The catalog endpoint is small and fast; unlike a
// consultation there is no reason to wait minutes for it.
const catalogTimeout = 30 * time.Second

func cmdModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: council models [flags]

Shows the council: the current frontier model from each lab, discovered from
OpenRouter's "~author/family-latest" aliases and cached for 24 hours.

Flags:
`)
		fs.PrintDefaults()
	}
	var (
		all     = fs.Bool("all", false, "list every ~latest alias, not just the seated council")
		refresh = fs.Bool("refresh", false, "refetch the catalog, ignoring the cache")
		asJSON  = fs.Bool("json", false, "emit JSON")
		noColor = fs.Bool("no-color", false, "disable ANSI colour")
	)
	if err := fs.Parse(args); err != nil {
		return &errUsage{}
	}

	res, err := loadRoster(*refresh)
	if err != nil {
		return err
	}

	if *asJSON {
		return render.RosterJSON(os.Stdout, res, *all)
	}
	style := render.AutoStyle(os.Stdout)
	if *noColor {
		style.Color = false
	}
	return render.Roster(os.Stdout, res.Roster, res, style, *all)
}

func cmdSkill(args []string) error {
	fs := flag.NewFlagSet("skill", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: council skill [flags]

Writes a SKILL.md that teaches a coding agent when to escalate a question to the
council and how to read the result. The current roster is baked into the output.

  council skill -o .claude/skills/council/SKILL.md

Flags:
`)
		fs.PrintDefaults()
	}
	var (
		out     = fs.String("o", "", "write to this file instead of stdout")
		refresh = fs.Bool("refresh", false, "refetch the catalog before generating")
	)
	if err := fs.Parse(args); err != nil {
		return &errUsage{}
	}

	res, err := loadRoster(*refresh)
	if err != nil {
		return err
	}

	if *out == "" {
		return render.Skill(os.Stdout, res.Roster)
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := render.Skill(f, res.Roster); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "council: wrote %s\n", *out)
	return f.Close()
}

// loadRoster runs discovery with a short timeout, shared by the models and skill
// commands. Both need only the catalog, never the API key for a consultation — but
// the same client is used, so a key is passed when one is available.
func loadRoster(refresh bool) (*registry.Result, error) {
	ctx, stop := signalContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()

	// The catalog endpoint is public. Missing credentials must not block discovery.
	apiKey, _ := config.APIKey()
	return newCatalog(openrouter.New(apiKey)).Load(ctx, refresh)
}
