package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/config"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/council"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/render"
)

// minSafeMaxTokens is the floor below which -max-tokens starts starving reasoning
// models of the budget they need to emit any prose at all. Measured: a 600-token cap
// made a frontier panellist return empty and be reported as failed.
const minSafeMaxTokens = 2000

func cmdAsk(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: council ask <question> [flags]

Puts one question to every council member in parallel, has a judge model compare
their answers, and reports the comparison. Pass "-" or no question to read from stdin.

Flags:
`)
		fs.PrintDefaults()
	}

	var (
		format      = fs.String("format", "human", "output format: human or json")
		asJSON      = fs.Bool("json", false, "shorthand for -format json")
		full        = fs.Bool("full", false, "include every panellist's complete answer")
		modelsFlag  = fs.String("models", "", "comma-separated panel, overriding discovery")
		judge       = fs.String("judge", "", "model that compares the panel (default: first panel member)")
		outer       = fs.String("outer", "", "model that writes the synthesis (default: the judge)")
		system      = fs.String("system", "", "system prompt for the synthesising model")
		maxTools    = fs.Int("max-tool-calls", council.DefaultMaxToolCalls, "web-research steps per model (1-16); lower is cheaper")
		maxTokens   = fs.Int("max-tokens", 0, "cap each panel and judge answer (0 = provider default)")
		temperature = fs.Float64("temperature", -1, "panel temperature (default: provider default)")
		timeout     = fs.Duration("timeout", council.DefaultTimeout, "abandon the consultation after this long")
		refresh     = fs.Bool("refresh", false, "refresh the model catalog before asking")
		rawOut      = fs.String("raw", "", "also write the unmodified API response to this file")
		noColor     = fs.Bool("no-color", false, "disable ANSI colour")
	)
	positionals, err := parseInterspersed(fs, args)
	if err != nil {
		return &errUsage{}
	}

	question, err := readQuestion(positionals, os.Stdin)
	if err != nil {
		return err
	}
	if *format != "human" && *format != "json" {
		return &errUsage{msg: fmt.Sprintf("unknown format %q: want human or json", *format)}
	}
	if *asJSON {
		*format = "json"
	}
	if *maxTools < 1 || *maxTools > 16 {
		return &errUsage{msg: fmt.Sprintf("-max-tool-calls must be between 1 and 16, got %d", *maxTools)}
	}
	// Panellists are reasoning models. A low cap is spent on reasoning tokens before
	// any prose is emitted, and the panellist is then reported as having failed with
	// "Stream completed without producing any text" — a silent, self-inflicted
	// degradation that looks like an upstream fault.
	if *maxTokens > 0 && *maxTokens < minSafeMaxTokens {
		fmt.Fprintf(os.Stderr,
			"council: warning: -max-tokens %d is low; reasoning models may return no text and be counted as failures (%d+ is safer)\n",
			*maxTokens, minSafeMaxTokens)
	}

	apiKey, err := config.APIKey()
	if err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	client := openrouter.New(apiKey)

	// Discovery is skipped entirely when the panel is given explicitly, so an
	// override works even if the catalog endpoint is unreachable.
	req := council.Request{
		Question:            question,
		Panel:               splitList(*modelsFlag),
		Judge:               *judge,
		Outer:               *outer,
		System:              *system,
		MaxToolCalls:        *maxTools,
		MaxCompletionTokens: *maxTokens,
	}
	if *temperature >= 0 {
		req.Temperature = temperature
	}

	if len(req.Panel) == 0 {
		res, err := newCatalog(client).Load(ctx, *refresh)
		if err != nil {
			return err
		}
		req.Roster = res.Roster
		req.Warnings = res.Roster.Warnings
		if res.Stale {
			req.Warnings = append(req.Warnings, "the model catalog is stale; the roster may be out of date")
		}
	}

	if *format == "human" {
		announce(os.Stderr, &req, *timeout)
	}

	consultation, raw, askErr := council.Ask(ctx, client, req)
	if len(raw) > 0 && *rawOut != "" {
		// Redact before writing: --raw exists for debugging and gets pasted around.
		safe := config.Redact(string(raw), apiKey)
		if err := os.WriteFile(*rawOut, []byte(safe), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "council: could not write %s: %v\n", *rawOut, err)
		}
	}
	if askErr != nil {
		return askErr
	}

	if *format == "json" {
		return render.JSON(os.Stdout, consultation)
	}
	style := render.AutoStyle(os.Stdout)
	if *noColor {
		style.Color = false
	}
	return render.Human(os.Stdout, consultation, style, *full)
}

// announce tells the user what is about to happen on stderr, because a consultation
// takes minutes and otherwise looks like a hang. stderr keeps stdout clean for pipes.
func announce(w io.Writer, req *council.Request, timeout time.Duration) {
	panel := req.Panel
	if len(panel) == 0 && req.Roster != nil {
		panel = req.Roster.Slugs()
	}
	fmt.Fprintf(w, "Convening %d models (timeout %s). This usually takes a few minutes.\n",
		len(panel), timeout)
	for _, slug := range panel {
		fmt.Fprintf(w, "  · %s\n", slug)
	}
	for _, warning := range req.Warnings {
		fmt.Fprintf(w, "  ! %s\n", warning)
	}
	fmt.Fprintln(w)
}

// readQuestion takes the question from the arguments, or from stdin when the
// arguments are empty or exactly "-".
func readQuestion(args []string, stdin io.Reader) (string, error) {
	joined := strings.TrimSpace(strings.Join(args, " "))
	if joined != "" && joined != "-" {
		return joined, nil
	}

	// Only read stdin when it is actually piped; an interactive terminal would hang.
	if f, ok := stdin.(*os.File); ok && joined == "" {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", &errUsage{msg: "no question given: pass one as an argument or pipe it on stdin"}
		}
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("reading the question from stdin: %w", err)
	}
	question := strings.TrimSpace(string(data))
	if question == "" {
		return "", &errUsage{msg: "no question given: pass one as an argument or pipe it on stdin"}
	}
	return question, nil
}
