package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points the config directory at a temp dir and clears the environment key,
// so these tests never read or write the developer's real configuration.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(EnvKey, "")
	return filepath.Join(dir, "council", KeyFileName)
}

func TestSaveAPIKeyCreatesFileWithRestrictivePermissions(t *testing.T) {
	want := isolate(t)

	got, err := SaveAPIKey("sk-or-v1-secret")
	if err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}

	info, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	// The file holds a credential; anything group- or world-readable is a bug.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(got))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %o, want 700", perm)
	}

	key, source, err := APIKeyWithSource()
	if err != nil {
		t.Fatalf("APIKeyWithSource after save: %v", err)
	}
	if key != "sk-or-v1-secret" {
		t.Errorf("round-tripped key = %q", key)
	}
	if source != want {
		t.Errorf("source = %q, want %q", source, want)
	}
}

func TestSaveAPIKeyPreservesUnrelatedLines(t *testing.T) {
	path := isolate(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, path, strings.Join([]string{
		"# my council config",
		"SOMETHING_ELSE=keep-me",
		"OPENROUTER_API_KEY=sk-or-v1-old",
		"TRAILING=also-keep",
	}, "\n")+"\n")

	if _, err := SaveAPIKey("sk-or-v1-new"); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, must := range []string{"# my council config", "SOMETHING_ELSE=keep-me", "TRAILING=also-keep", "OPENROUTER_API_KEY=sk-or-v1-new"} {
		if !strings.Contains(got, must) {
			t.Errorf("result is missing %q:\n%s", must, got)
		}
	}
	if strings.Contains(got, "sk-or-v1-old") {
		t.Errorf("old key survived:\n%s", got)
	}
}

func TestReplaceAssignment(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"empty file", "", "OPENROUTER_API_KEY=new\n"},
		{"no trailing newline", "OTHER=1", "OTHER=1\nOPENROUTER_API_KEY=new\n"},
		{"replaces in place", "A=1\nOPENROUTER_API_KEY=old\nB=2\n", "A=1\nOPENROUTER_API_KEY=new\nB=2\n"},
		// A shell may source this file, so an export prefix the user chose is kept.
		{"keeps export prefix", "export OPENROUTER_API_KEY=old\n", "export OPENROUTER_API_KEY=new\n"},
		// A second assignment would win when the file is sourced, silently shadowing
		// the key that was just written.
		{"blanks duplicate assignments", "OPENROUTER_API_KEY=old\nOPENROUTER_API_KEY=older\n", "OPENROUTER_API_KEY=new\n\n"},
		{"ignores commented assignment", "#OPENROUTER_API_KEY=old\n", "#OPENROUTER_API_KEY=old\nOPENROUTER_API_KEY=new\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := replaceAssignment(tt.body, EnvKey, "new"); got != tt.want {
				t.Errorf("replaceAssignment(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestSaveAPIKeyRejectsUnusableValues(t *testing.T) {
	isolate(t)
	for _, bad := range []string{"", "   ", "sk-or-v1-a\nOTHER=injected"} {
		if _, err := SaveAPIKey(bad); err == nil {
			t.Errorf("SaveAPIKey(%q) succeeded, want an error", bad)
		}
	}
}

func TestSaveAPIKeyIsAtomic(t *testing.T) {
	path := isolate(t)
	if _, err := SaveAPIKey("sk-or-v1-first"); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveAPIKey("sk-or-v1-second"); err != nil {
		t.Fatal(err)
	}
	// A temp file left behind would be a credential sitting in the config directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected exactly the config file, got %d entries", len(entries))
	}
}

func TestShadowingReportsHigherPrecedenceSources(t *testing.T) {
	path := isolate(t)
	if _, err := SaveAPIKey("sk-or-v1-global"); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	chdir(t, work)

	if got := Shadowing(); len(got) != 0 {
		t.Errorf("nothing should shadow a freshly saved key, got %v", got)
	}

	writeEnv(t, filepath.Join(work, ".env"), "OPENROUTER_API_KEY=sk-or-v1-local")
	if got := Shadowing(); len(got) != 1 || got[0] != ".env" {
		t.Errorf("Shadowing() = %v, want [.env]", got)
	}

	t.Setenv(EnvKey, "sk-or-v1-env")
	got := Shadowing()
	if len(got) != 2 || got[0] != SourceEnvironment || got[1] != ".env" {
		t.Errorf("Shadowing() = %v, want [environment .env] in precedence order", got)
	}

	// And the shadowing source is genuinely the one that wins.
	key, source, err := APIKeyWithSource()
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-or-v1-env" || source != SourceEnvironment {
		t.Errorf("APIKeyWithSource() = %q from %q, want the environment value", key, source)
	}
	_ = path
}
