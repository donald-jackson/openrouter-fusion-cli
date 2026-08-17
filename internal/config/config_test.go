package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := strings.Join([]string{
		"# a comment",
		"",
		"OPENROUTER_API_KEY=sk-or-v1-plain",
		`QUOTED="sk-or-v1-quoted"`,
		"SINGLE='sk-or-v1-single'",
		"export EXPORTED=value",
		"  SPACED  =  padded  ",
		"malformed-line-without-equals",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	vals, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}

	want := map[string]string{
		"OPENROUTER_API_KEY": "sk-or-v1-plain",
		"QUOTED":             "sk-or-v1-quoted",
		"SINGLE":             "sk-or-v1-single",
		"EXPORTED":           "value",
		"SPACED":             "padded",
	}
	for k, w := range want {
		if got := vals[k]; got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
	if _, ok := vals["malformed-line-without-equals"]; ok {
		t.Error("malformed line should be skipped")
	}
}

func TestAPIKeyPrefersEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, filepath.Join(dir, ".env"), "OPENROUTER_API_KEY=from-file")
	chdir(t, dir)

	t.Setenv(EnvKey, "from-environment")
	got, err := APIKey()
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-environment" {
		t.Errorf("APIKey() = %q, want the environment value", got)
	}
}

func TestAPIKeyFallsBackToDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, filepath.Join(dir, ".env"), "OPENROUTER_API_KEY=from-file")
	chdir(t, dir)

	t.Setenv(EnvKey, "")
	got, err := APIKey()
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-file" {
		t.Errorf("APIKey() = %q, want the .env value", got)
	}
}

func TestAPIKeyMissing(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv(EnvKey, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := APIKey()
	var missing *ErrNoAPIKey
	if err == nil {
		t.Fatal("expected an error when no key is available")
	}
	if !asErrNoAPIKey(err, &missing) {
		t.Fatalf("expected *ErrNoAPIKey, got %T", err)
	}
	if !strings.Contains(err.Error(), EnvKey) {
		t.Errorf("error should name the env var, got %q", err)
	}
}

func TestRedact(t *testing.T) {
	secret := "sk-or-v1-abcdefghijklmnop"
	got := Redact("Authorization: Bearer "+secret+" trailing", secret)
	if strings.Contains(got, secret) {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Errorf("expected a redaction marker, got %q", got)
	}

	// A short secret must be fully masked, never partially echoed.
	short := "abc"
	if out := Redact("key="+short, short); strings.Contains(out, "abc") {
		t.Errorf("short secret leaked: %q", out)
	}

	// An empty secret must leave the input untouched.
	if out := Redact("unchanged", ""); out != "unchanged" {
		t.Errorf("Redact with empty secret = %q", out)
	}
}

func TestCacheDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	got, err := CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/custom/cache", "council"); got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
}

func writeEnv(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func chdir(t *testing.T, dir string) {
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

func asErrNoAPIKey(err error, target **ErrNoAPIKey) bool {
	e, ok := err.(*ErrNoAPIKey)
	if ok {
		*target = e
	}
	return ok
}
