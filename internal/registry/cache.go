package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/donald-jackson/openrouter-fusion-cli/internal/config"
	"github.com/donald-jackson/openrouter-fusion-cli/internal/openrouter"
)

// TTL is how long a discovered catalog stays fresh.
const TTL = 24 * time.Hour

// cacheFile is the catalog cache filename inside the cache directory.
const cacheFile = "models.json"

// entry is the on-disk cache record. The catalog is stored as the raw API body so a
// change to the Model struct cannot corrupt or silently truncate what was cached.
type entry struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Body      json.RawMessage `json:"body"`
}

// Catalog fetches the model catalog, preferring a cache entry younger than TTL.
type Catalog struct {
	Client *openrouter.Client
	// Dir is the cache directory. When empty, the user cache directory is used.
	Dir string
	// Now supplies the current time; tests substitute it.
	Now func() time.Time
	// Warn receives non-fatal messages, such as a failed refresh served from stale cache.
	Warn func(string)
}

// Result carries a discovered roster and where it came from.
type Result struct {
	Roster    *Roster
	FetchedAt time.Time
	// FromCache is true when no network request was made.
	FromCache bool
	// Stale is true when the cache was past its TTL but served anyway because the
	// refresh failed.
	Stale bool
}

// Age returns how long ago the underlying catalog was fetched.
func (r Result) Age() time.Duration { return time.Since(r.FetchedAt) }

// Expires returns when the cached catalog stops being fresh.
func (r Result) Expires() time.Time { return r.FetchedAt.Add(TTL) }

// Load returns the council roster. Unless refresh is set, a cache entry younger than
// TTL is used without touching the network. If a refresh is needed but fails, any
// existing cache entry is served stale with a warning — a network blip should degrade
// discovery, not block a consultation.
func (c *Catalog) Load(ctx context.Context, refresh bool) (*Result, error) {
	now := c.now()

	cached, cachedErr := c.read()
	if !refresh && cachedErr == nil && now.Sub(cached.FetchedAt) < TTL {
		roster, err := buildFrom(cached.Body)
		if err == nil {
			return &Result{Roster: roster, FetchedAt: cached.FetchedAt, FromCache: true}, nil
		}
		// A corrupt cache is not fatal; fall through and refetch.
		c.warn(fmt.Sprintf("ignoring unusable cache: %v", err))
	}

	catalog, raw, err := c.Client.Models(ctx)
	if err != nil {
		if cachedErr == nil {
			if roster, bErr := buildFrom(cached.Body); bErr == nil {
				c.warn(fmt.Sprintf("could not refresh the model catalog (%v); using the copy cached %s ago",
					err, now.Sub(cached.FetchedAt).Round(time.Minute)))
				return &Result{Roster: roster, FetchedAt: cached.FetchedAt, FromCache: true, Stale: true}, nil
			}
		}
		return nil, fmt.Errorf("fetching model catalog: %w", err)
	}

	roster, err := Build(catalog)
	if err != nil {
		return nil, err
	}
	if err := c.write(entry{FetchedAt: now, Body: raw}); err != nil {
		c.warn(fmt.Sprintf("could not write the catalog cache: %v", err))
	}
	return &Result{Roster: roster, FetchedAt: now}, nil
}

func buildFrom(body []byte) (*Roster, error) {
	catalog, err := openrouter.ParseModels(body)
	if err != nil {
		return nil, err
	}
	return Build(catalog)
}

func (c *Catalog) dir() (string, error) {
	if c.Dir != "" {
		return c.Dir, nil
	}
	return config.CacheDir()
}

func (c *Catalog) read() (entry, error) {
	var e entry
	dir, err := c.dir()
	if err != nil {
		return e, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, cacheFile))
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, err
	}
	if len(e.Body) == 0 {
		return e, fmt.Errorf("cache entry has no catalog body")
	}
	return e, nil
}

// write persists the cache entry atomically: a temp file in the destination directory
// followed by a rename, so a crash or a concurrent reader never sees a half-written file.
func (c *Catalog) write(e entry) error {
	dir, err := c.dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, cacheFile+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, cacheFile))
}

func (c *Catalog) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Catalog) warn(msg string) {
	if c.Warn != nil {
		c.Warn(msg)
	}
}
