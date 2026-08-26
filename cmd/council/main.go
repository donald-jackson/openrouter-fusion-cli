// Command council asks a panel of frontier models one question and reports where
// they agreed, where they contradicted each other, and what none of them covered.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/config"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/council"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/registry"
)

// version is overridable at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// Exit codes, so scripts and agents can branch on the failure mode.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitNoAPIKey    = 3
	exitAPIError    = 4
	exitFusionFail  = 5
	exitInterrupted = 6
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return exitUsage
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "ask":
		return report(cmdAsk(rest))
	case "models":
		return report(cmdModels(rest))
	case "skill":
		return report(cmdSkill(rest))
	case "setup":
		return report(cmdSetup(rest))
	case "version", "--version", "-v":
		fmt.Println("council", version)
		return exitOK
	case "help", "--help", "-h":
		usage(os.Stdout)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "council: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		return exitUsage
	}
}

// report maps an error to an exit code, printing it unless it is a usage error the
// flag package has already described.
func report(err error) int {
	if err == nil {
		return exitOK
	}

	var noKey *config.ErrNoAPIKey
	var apiErr *openrouter.APIError
	var fusionErr *council.ErrFusionFailed
	var usageErr *errUsage

	switch {
	case errors.As(err, &usageErr):
		if usageErr.msg != "" {
			fmt.Fprintf(os.Stderr, "council: %s\n", usageErr.msg)
		}
		return exitUsage
	case errors.As(err, &noKey):
		fmt.Fprintf(os.Stderr, "council: %v\n", err)
		fmt.Fprintln(os.Stderr, "  run `council setup` to store one, or set "+config.EnvKey+" in the environment.")
		return exitNoAPIKey
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintln(os.Stderr, "council: cancelled or timed out before the panel finished")
		return exitInterrupted
	case errors.As(err, &fusionErr):
		fmt.Fprintf(os.Stderr, "council: %v\n", err)
		return exitFusionFail
	case errors.Is(err, council.ErrPanelNotConvened):
		fmt.Fprintf(os.Stderr, "council: %v\n", err)
		fmt.Fprintln(os.Stderr, "  the answer would have come from a single model, so it was discarded.")
		return exitError
	case errors.As(err, &apiErr):
		fmt.Fprintf(os.Stderr, "council: %v\n", err)
		return exitAPIError
	default:
		fmt.Fprintf(os.Stderr, "council: %v\n", err)
		return exitError
	}
}

// errUsage marks an error as a usage problem rather than a runtime failure.
type errUsage struct{ msg string }

func (e *errUsage) Error() string { return e.msg }

func usage(w io.Writer) {
	fmt.Fprint(w, `council — ask a panel of frontier models one question, and see where they disagree.

Usage:
  council ask <question>     Convene the council. Use "-" to read from stdin.
  council models             Show the current council and cache status.
  council skill              Emit a SKILL.md teaching an agent to use this tool.
  council setup              Store your OpenRouter API key for use from any directory.
  council version

Run "council <command> -h" for the flags of each command.

A consultation runs every panel member plus a judge and a synthesis pass, each with
web search enabled. Expect minutes and dollars, not seconds and cents.
`)
}

// signalContext cancels on SIGINT/SIGTERM so a long consultation can be abandoned.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// newCatalog builds the discovery layer, routing warnings to stderr so they never
// contaminate stdout — stdout may be piped into a JSON parser.
func newCatalog(client *openrouter.Client) *registry.Catalog {
	return &registry.Catalog{
		Client: client,
		Warn:   func(msg string) { fmt.Fprintf(os.Stderr, "council: %s\n", msg) },
	}
}

// parseInterspersed parses fs even when flags follow positional arguments, and
// returns the positionals.
//
// Go's flag package stops at the first non-flag argument, which for this CLI is a
// silent trap: `council ask "question" --full` would parse no flags and fold "--full"
// into the question itself, sending it to the models and billing for the run. Parsing
// in passes — positionals, then any flags after them — makes argument order irrelevant.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

// splitList parses a comma-separated flag value, ignoring empty entries.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
