package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/config"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
)

// setupTimeout bounds the validation call. GET /api/v1/key is a metadata lookup, so
// anything slower than this is a network problem rather than a slow response.
const setupTimeout = 20 * time.Second

// keyPrefix is the prefix every OpenRouter inference key has carried. A mismatch is
// warned about but not rejected: the live check is authoritative, and hard-coding a
// prefix would break the CLI the day OpenRouter changes it.
const keyPrefix = "sk-or-v1-"

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Usage: council setup [flags]

Stores your OpenRouter API key so that council works from any directory, without a
.env file and without exporting anything in your shell.

The key is read without echoing it to the terminal, checked against OpenRouter, and
written to %s with permissions 0600.

Get a key at https://openrouter.ai/keys

For unattended use, pipe the key in instead:

  echo "$OPENROUTER_API_KEY" | council setup

Flags:
`, displayKeyPath())
		fs.PrintDefaults()
	}
	var (
		force    = fs.Bool("force", false, "replace an existing saved key without asking")
		noVerify = fs.Bool("no-verify", false, "skip the check that the key works")
		show     = fs.Bool("show", false, "report where the key is currently read from, and change nothing")
	)
	if err := fs.Parse(args); err != nil {
		return &errUsage{}
	}
	if fs.NArg() > 0 {
		return &errUsage{msg: fmt.Sprintf("setup takes no arguments (got %q)", fs.Arg(0))}
	}

	if *show {
		return reportKeySource(os.Stdout)
	}

	path, err := config.KeyPath()
	if err != nil {
		return err
	}

	// Prompts, progress and warnings go to stderr so that stdout stays empty on
	// success — setup is scriptable, and nothing should have to be filtered out.
	out := os.Stderr

	if existing := savedKey(path); existing != "" && !*force {
		// Unattended runs must not silently do nothing: a script that pipes a key in
		// would read a success exit code and believe the key had been stored.
		if !isCharDevice(os.Stdin) {
			return &errUsage{msg: fmt.Sprintf(
				"%s already holds a key (%s); pass --force to replace it", path, mask(existing))}
		}
		fmt.Fprintf(out, "council: %s already holds a key (%s)\n", path, mask(existing))
		if !confirm(out, "Replace it?") {
			fmt.Fprintln(out, "council: left unchanged")
			return nil
		}
	}

	key, err := readSecret(out, "OpenRouter API key (hidden): ")
	if err != nil {
		if errors.Is(err, io.EOF) {
			return &errUsage{msg: "no key supplied"}
		}
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return &errUsage{msg: "no key supplied"}
	}
	if !strings.HasPrefix(key, keyPrefix) {
		fmt.Fprintf(out, "council: warning: keys usually start with %q — checking it anyway\n", keyPrefix)
	}

	if !*noVerify {
		info, err := verifyKey(key)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "council: key accepted by OpenRouter — %s, %s\n", info.Label, info.Spend())
		if info.IsProvisioning {
			return fmt.Errorf("that is a provisioning key, which manages other keys and cannot run inference;\n" +
				"  create an inference key at https://openrouter.ai/keys")
		}
	}

	written, err := config.SaveAPIKey(key)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "council: saved to %s (mode 0600)\n", written)

	warnShadowed(out, written)
	return nil
}

// verifyKey checks a key against the API before it is written to disk, so a typo is
// caught here rather than at the start of a consultation.
func verifyKey(key string) (*openrouter.KeyInfo, error) {
	ctx, stop := signalContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()

	info, err := openrouter.New(key).Key(ctx)
	if err == nil {
		return info, nil
	}

	var apiErr *openrouter.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
		return nil, fmt.Errorf("OpenRouter rejected that key (%s);\n"+
			"  check it at https://openrouter.ai/keys, or re-run with --no-verify to save it anyway"+
			"%w", apiErr.Message, suppressed{apiErr})
	}
	return nil, fmt.Errorf("could not check the key: %w\n"+
		"  re-run with --no-verify to save it without checking", err)
}

// warnShadowed reports any location that would win over the file just written. Saving
// a key that something closer overrides is a silent no-op from the user's point of view.
func warnShadowed(w io.Writer, written string) {
	shadows := config.Shadowing()
	if len(shadows) == 0 {
		fmt.Fprintln(w, "council: ready — try `council models`, then `council ask \"<question>\"`")
		return
	}
	verb := "takes"
	if len(shadows) > 1 {
		verb = "take"
	}
	fmt.Fprintf(w, "council: note: %s still %s precedence over %s,\n",
		strings.Join(shadows, " and "), verb, written)
	fmt.Fprintln(w, "  so the saved key is a fallback until that is removed.")
}

// reportKeySource implements --show: which key is in effect and where it comes from.
func reportKeySource(w io.Writer) error {
	path, err := config.KeyPath()
	if err != nil {
		return err
	}
	key, source, err := config.APIKeyWithSource()
	if err != nil {
		fmt.Fprintf(w, "no key found\n  global config: %s (not present)\n", path)
		fmt.Fprintln(w, "  run `council setup` to create it")
		return nil
	}
	fmt.Fprintf(w, "key %s\n  in use from: %s\n  global config: %s\n", mask(key), source, path)
	if shadows := config.Shadowing(); len(shadows) > 0 {
		fmt.Fprintf(w, "  shadowing the global config: %s\n", strings.Join(shadows, ", "))
	}
	return nil
}

// savedKey returns the key currently in the global config file, ignoring every other
// location, so the overwrite prompt describes that file and nothing else.
func savedKey(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if ok && strings.TrimSpace(k) == config.EnvKey {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// mask renders a key for display: enough to recognise, never enough to use.
func mask(key string) string {
	if len(key) <= 12 {
		return "…"
	}
	return key[:10] + "…" + key[len(key)-4:]
}

// displayKeyPath is the config path for help text, degrading to the documented
// default when the home directory cannot be resolved.
func displayKeyPath() string {
	if p, err := config.KeyPath(); err == nil {
		return p
	}
	return "~/.config/council/" + config.KeyFileName
}

// suppressed carries an error through the chain for errors.As classification without
// contributing anything to the message. It lets a hand-written, actionable string be
// shown to the user while the underlying *openrouter.APIError still selects the exit
// code, instead of having to choose between a good message and a correct exit status.
type suppressed struct{ err error }

func (suppressed) Error() string   { return "" }
func (s suppressed) Unwrap() error { return s.err }
