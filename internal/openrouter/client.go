// Package openrouter is a minimal client for the two OpenRouter endpoints this CLI needs:
// GET /api/v1/models (the model catalog, free and unauthenticated) and
// POST /api/v1/responses (the Responses API, used with the openrouter:fusion server tool).
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// DefaultBaseURL is the OpenRouter API root.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// maxAttempts bounds retries of transient failures (429 and 5xx).
const maxAttempts = 3

// maxResponseBytes caps how much of a response body we will buffer, so a runaway
// or hostile response cannot exhaust memory.
const maxResponseBytes = 64 << 20 // 64 MiB

// Client talks to the OpenRouter API. The zero value is not usable; use New.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client

	// Referer and Title populate OpenRouter's optional attribution headers.
	Referer string
	Title   string
}

// New returns a Client with sensible defaults. Per-request deadlines come from the
// context rather than a client-wide timeout, because a fusion consultation legitimately
// runs for minutes while a catalog fetch should be quick.
func New(apiKey string) *Client {
	return &Client{
		APIKey:  apiKey,
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{},
		Referer: "https://github.com/donald-jackson/openrouter-fusion-cli",
		Title:   "council",
	}
}

// APIError is a structured error returned by the OpenRouter API.
type APIError struct {
	StatusCode int
	Code       int    `json:"code"`
	Message    string `json:"message"`
	Metadata   any    `json:"metadata,omitempty"`
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("openrouter: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("openrouter: HTTP %d: %s", e.StatusCode, e.Message)
}

// errorEnvelope matches OpenRouter's {"error": {...}} response shape.
type errorEnvelope struct {
	Error *APIError `json:"error"`
}

// get performs an authenticated GET and returns the raw body.
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

// post performs an authenticated POST of a JSON payload and returns the raw body.
func (c *Client) post(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	return c.do(ctx, http.MethodPost, path, body)
}

// do issues the request, retrying transient failures, and returns the raw response body.
//
// OpenRouter pads slow responses with whitespace keep-alives ahead of the JSON body.
// Returning raw bytes and letting callers use encoding/json handles that transparently,
// since the JSON decoders skip leading whitespace.
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := sleep(ctx, backoff(attempt, lastErr)); err != nil {
				return nil, err
			}
		}

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body) // fresh reader per attempt
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
		if err != nil {
			return nil, err
		}
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		if c.Referer != "" {
			req.Header.Set("HTTP-Referer", c.Referer)
		}
		if c.Title != "" {
			req.Header.Set("X-Title", c.Title)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			// A cancelled or expired context is final, never transient.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = readErr
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// A 200 can still carry an error envelope.
			if apiErr := decodeError(raw, resp.StatusCode); apiErr != nil {
				return raw, apiErr
			}
			return raw, nil
		}

		apiErr := decodeError(raw, resp.StatusCode)
		if apiErr == nil {
			apiErr = &APIError{StatusCode: resp.StatusCode, Message: snippet(raw)}
		}
		if !retryable(resp.StatusCode) {
			return raw, apiErr
		}
		apiErr.Metadata = retryAfter(resp.Header)
		lastErr = apiErr
	}

	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

// decodeError returns an *APIError if raw carries an OpenRouter error envelope.
func decodeError(raw []byte, status int) *APIError {
	var env errorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Error == nil {
		return nil
	}
	if env.Error.Message == "" && env.Error.Code == 0 {
		return nil
	}
	env.Error.StatusCode = status
	return env.Error
}

// retryable reports whether a status code represents a transient failure.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// backoff returns the delay before the given attempt, honouring a Retry-After hint
// carried on the previous error and otherwise growing exponentially with jitter.
func backoff(attempt int, lastErr error) time.Duration {
	if apiErr, ok := lastErr.(*APIError); ok {
		if d, ok := apiErr.Metadata.(time.Duration); ok && d > 0 {
			return d
		}
	}
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	return base + time.Duration(rand.N(500*int64(time.Millisecond)))
}

// retryAfter parses a Retry-After header in either delay-seconds or HTTP-date form.
func retryAfter(h http.Header) any {
	v := h.Get("Retry-After")
	if v == "" {
		return nil
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// snippet trims a response body down to something safe to embed in an error message.
func snippet(raw []byte) string {
	const limit = 200
	s := string(bytes.TrimSpace(raw))
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
