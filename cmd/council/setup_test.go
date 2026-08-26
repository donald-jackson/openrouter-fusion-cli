package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/config"
)

func TestMaskNeverRevealsAWholeKey(t *testing.T) {
	const key = "sk-or-v1-EXAMPLEnotarealkeyEXAMPLEnotarealkeyEXAMPLEnotarealkeyEXAMPLEnot"
	got := mask(key)
	if strings.Contains(got, key) {
		t.Fatalf("mask leaked the key: %q", got)
	}
	if len(got) >= len(key) {
		t.Errorf("mask(%d chars) produced %d chars", len(key), len(got))
	}
	if !strings.HasPrefix(got, "sk-or-v1-E") {
		t.Errorf("mask should stay recognisable, got %q", got)
	}
	// Short strings must not be sliced into a panic, or partially revealed.
	for _, short := range []string{"", "abc", "sk-or-v1-abc"} {
		if m := mask(short); strings.Contains(m, "abc") && short != "" {
			t.Errorf("mask(%q) = %q leaked a short key", short, m)
		}
	}
}

func TestSavedKeyReadsOnlyTheGlobalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.env")

	if got := savedKey(path); got != "" {
		t.Errorf("missing file should yield %q, got %q", "", got)
	}

	content := strings.Join([]string{
		"# comment",
		"OTHER=value",
		`export OPENROUTER_API_KEY="sk-or-v1-quoted"`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := savedKey(path), "sk-or-v1-quoted"; got != want {
		t.Errorf("savedKey = %q, want %q", got, want)
	}
}

func TestSetupRejectsArguments(t *testing.T) {
	err := cmdSetup([]string{"some-key-pasted-as-an-argument"})
	if err == nil {
		t.Fatal("passing the key as an argument should be a usage error")
	}
	var usageErr *errUsage
	if !errors.As(err, &usageErr) {
		t.Fatalf("error = %v, want a usage error", err)
	}
	// The key must not be echoed back in the error, in case it really was a key.
	if strings.Contains(usageErr.msg, "sk-or-v1") {
		t.Errorf("usage message leaked a credential: %q", usageErr.msg)
	}
}

func TestSetupShowReportsSource(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv(config.EnvKey, "")

	var buf bytes.Buffer
	if err := reportKeySource(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no key found") {
		t.Errorf("with nothing configured, got:\n%s", buf.String())
	}

	if _, err := config.SaveAPIKey("sk-or-v1-abcdefghijklmnop"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvKey, "sk-or-v1-zzzzzzzzzzzzzzzz")

	buf.Reset()
	if err := reportKeySource(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "in use from: "+config.SourceEnvironment) {
		t.Errorf("should report the environment as the live source:\n%s", out)
	}
	if !strings.Contains(out, "shadowing the global config") {
		t.Errorf("should warn that the saved key is being shadowed:\n%s", out)
	}
	if strings.Contains(out, "sk-or-v1-zzzzzzzzzzzzzzzz") {
		t.Errorf("--show printed a full key:\n%s", out)
	}
}

func TestWarnShadowedAgreesWithItsSubject(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv(config.EnvKey, "")
	work := t.TempDir()
	chdirT(t, work)

	var buf bytes.Buffer
	warnShadowed(&buf, "/tmp/config.env")
	if !strings.Contains(buf.String(), "ready") {
		t.Errorf("unshadowed save should confirm readiness:\n%s", buf.String())
	}

	if err := os.WriteFile(filepath.Join(work, ".env"), []byte("OPENROUTER_API_KEY=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	warnShadowed(&buf, "/tmp/config.env")
	if !strings.Contains(buf.String(), ".env still takes precedence") {
		t.Errorf("single shadow should read 'takes':\n%s", buf.String())
	}

	t.Setenv(config.EnvKey, "y")
	buf.Reset()
	warnShadowed(&buf, "/tmp/config.env")
	if !strings.Contains(buf.String(), "environment and .env still take precedence") {
		t.Errorf("two shadows should read 'take':\n%s", buf.String())
	}
}

func TestConfirmDefaultsToNoWhenNotATerminal(t *testing.T) {
	// stdin under `go test` is not a terminal, which is exactly the unattended case:
	// a prompt that cannot be answered must never be treated as a yes.
	if confirm(&bytes.Buffer{}, "Replace it?") {
		t.Error("confirm should refuse when it cannot ask")
	}
}

func TestReadSecretFromPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		w.WriteString("  sk-or-v1-piped  \n")
		w.Close()
	}()
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin })

	var out bytes.Buffer
	got, err := readSecret(&out, "should not be shown: ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-or-v1-piped" {
		t.Errorf("readSecret = %q, want the trimmed key", got)
	}
	// A piped run is a script; a prompt would end up in its logs.
	if out.Len() != 0 {
		t.Errorf("prompted despite stdin being a pipe: %q", out.String())
	}
}

func chdirT(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
}
