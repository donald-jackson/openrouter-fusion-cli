// Package config resolves the OpenRouter API key and the on-disk paths the CLI uses.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvKey is the environment variable holding the OpenRouter API key.
const EnvKey = "OPENROUTER_API_KEY"

// ErrNoAPIKey is returned when no key could be found in any of the supported locations.
type ErrNoAPIKey struct {
	Searched []string
}

func (e *ErrNoAPIKey) Error() string {
	return fmt.Sprintf("no %s found (looked in: %s)", EnvKey, strings.Join(e.Searched, ", "))
}

// APIKey resolves the API key from, in order: the environment, a .env file in the
// working directory, and ~/.config/council/config.env. The first non-empty value wins.
func APIKey() (string, error) {
	searched := []string{"environment"}
	if k := strings.TrimSpace(os.Getenv(EnvKey)); k != "" {
		return k, nil
	}

	for _, path := range keyFiles() {
		searched = append(searched, path)
		vals, err := parseEnvFile(path)
		if err != nil {
			continue // unreadable or absent; try the next location
		}
		if k := strings.TrimSpace(vals[EnvKey]); k != "" {
			return k, nil
		}
	}
	return "", &ErrNoAPIKey{Searched: searched}
}

func keyFiles() []string {
	paths := []string{".env"}
	if dir, err := ConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "config.env"))
	}
	return paths
}

// parseEnvFile reads a minimal .env file: KEY=VALUE lines, with blank lines and
// #-comments skipped, an optional "export " prefix, and surrounding quotes stripped.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vals := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(key)] = unquote(strings.TrimSpace(val))
	}
	return vals, sc.Err()
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// CacheDir returns the directory for the model catalog cache, honouring XDG_CACHE_HOME.
func CacheDir() (string, error) { return userDir("XDG_CACHE_HOME", ".cache") }

// ConfigDir returns the directory for user configuration, honouring XDG_CONFIG_HOME.
func ConfigDir() (string, error) { return userDir("XDG_CONFIG_HOME", ".config") }

func userDir(env, fallback string) (string, error) {
	if base := os.Getenv(env); base != "" {
		return filepath.Join(base, "council"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback, "council"), nil
}

// Redact replaces every occurrence of secret in s with a masked form, so that raw
// API payloads and error strings can be printed without leaking the key.
func Redact(s, secret string) string {
	if secret == "" {
		return s
	}
	mask := "[redacted]"
	if len(secret) > 8 {
		mask = secret[:8] + "…[redacted]"
	}
	return strings.ReplaceAll(s, secret, mask)
}
