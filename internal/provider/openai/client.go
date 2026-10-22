package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	// BaseURL is overridable so tests can point it at httptest.
	BaseURL = "https://api.openai.com/v1"
	// UserAgent is set by the CLI to "keywatch/<version>".
	UserAgent = "keywatch/dev"
	// Logf logs one line per network call: method, host and path only.
	// Never headers, query values or bodies. No-op unless the CLI sets it.
	Logf = func(format string, a ...any) {}
	// HTTPClient is the client every call uses.
	HTTPClient = &http.Client{Timeout: 30 * time.Second}
)

// APIError is a non-2xx response. Message is the provider's own error text.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("openai: HTTP %d: %s", e.Status, e.Message)
}

// call sends one request and decodes a JSON response into out (if non-nil).
func call(ctx context.Context, adminKey, method, path string, q url.Values, body, out any) error {
	u, err := url.Parse(BaseURL + path)
	if err != nil {
		return fmt.Errorf("openai: bad url: %w", err)
	}
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminKey)
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	Logf("%s %s%s", method, u.Host, u.Path)
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("openai: %s %s: %w", method, u.Path, unwrapURLError(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("openai: reading %s: %w", u.Path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("openai: decoding %s: %w", u.Path, err)
	}
	return nil
}

// unwrapURLError drops the full URL (with query) from transport errors so
// logs stay at method + host + path.
func unwrapURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

// windows splits [from, to) into chunks of at most max buckets of width step,
// so each request stays within the API's per-request bucket limit.
func windows(from, to time.Time, step time.Duration, max int) [][2]time.Time {
	var out [][2]time.Time
	span := step * time.Duration(max)
	for s := from; s.Before(to); s = s.Add(span) {
		e := s.Add(span)
		if e.After(to) {
			e = to
		}
		out = append(out, [2]time.Time{s, e})
	}
	return out
}
