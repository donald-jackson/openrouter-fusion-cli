package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyFileName is the config file the setup command writes.
const KeyFileName = "config.env"

// KeyPath returns the path of the global key file, whether or not it exists.
func KeyPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, KeyFileName), nil
}

// SaveAPIKey writes key to the global config file and returns the path written.
//
// Two properties matter here. The file is written atomically, so an interrupted write
// cannot leave a half-key behind that would then fail confusingly on the next run. And
// any lines the file already had are preserved — only the OPENROUTER_API_KEY assignment
// is replaced — because this file is the user's, and setup should not quietly discard
// something it does not understand.
func SaveAPIKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("refusing to save an empty API key")
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", fmt.Errorf("API key contains a line break")
	}

	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	// 0700: the directory holds a credential, so it should not be world-readable.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, KeyFileName)

	existing, _ := os.ReadFile(path) // absent is fine; we are creating it
	content := replaceAssignment(string(existing), EnvKey, key)

	tmp, err := os.CreateTemp(dir, KeyFileName+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	// CreateTemp already uses 0600, but be explicit: this file holds a secret.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil { // durable before the rename, not after
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return path, nil
}

// replaceAssignment returns body with the assignment of name set to value, replacing
// an existing assignment in place if there is one and appending otherwise. Matching
// mirrors parseEnvFile so that whatever setup writes is what APIKey later reads.
func replaceAssignment(body, name, value string) string {
	assignment := name + "=" + value
	if body == "" {
		return assignment + "\n"
	}

	lines := strings.Split(body, "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		k, _, ok := strings.Cut(strings.TrimPrefix(trimmed, "export "), "=")
		if !ok || strings.TrimSpace(k) != name {
			continue
		}
		if replaced {
			lines[i] = "" // a duplicate assignment would shadow the one we just wrote
			continue
		}
		// Keep an "export " prefix if the user had one; their shell may source this.
		if strings.HasPrefix(trimmed, "export ") {
			lines[i] = "export " + assignment
		} else {
			lines[i] = assignment
		}
		replaced = true
	}

	out := strings.Join(lines, "\n")
	if !replaced {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += assignment + "\n"
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}
