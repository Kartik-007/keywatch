package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
)

func init() { provider.Register(Provider{}) }

// Provider implements provider.Provider for OpenAI.
type Provider struct{}

func (Provider) Name() string { return core.OpenAI }

func (Provider) AdminKeyURL() string {
	return "https://platform.openai.com/settings/organization/admin-keys"
}

// Reversible is false: OpenAI can only delete a key, not deactivate it.
func (Provider) Reversible() bool { return false }

// Connect lists one project, the cheapest call that proves the key is an
// organization admin key.
func (Provider) Connect(ctx context.Context, adminKey string) error {
	err := call(ctx, adminKey, http.MethodGet, "/organization/projects", url.Values{"limit": {"1"}}, nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 {
		return fmt.Errorf("OpenAI rejected this key (HTTP %d: %s). keywatch needs an organization Admin key, not a project API key. An organization owner can create one at %s",
			ae.Status, ae.Message, Provider{}.AdminKeyURL())
	}
	return err
}

type listPage[T any] struct {
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

type project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiKey struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RedactedValue string `json:"redacted_value"`
	CreatedAt     int64  `json:"created_at"`
	LastUsedAt    *int64 `json:"last_used_at"`
	ExpiresAt     *int64 `json:"expires_at"`
}

// list follows after/has_more/last_id pagination.
func list[T any](ctx context.Context, adminKey, path string, q url.Values) ([]T, error) {
	var all []T
	q.Set("limit", "100")
	for {
		var page listPage[T]
		if err := call(ctx, adminKey, http.MethodGet, path, q, nil, &page); err != nil {
			return all, err
		}
		all = append(all, page.Data...)
		if !page.HasMore || page.LastID == "" {
			return all, nil
		}
		q.Set("after", page.LastID)
	}
}

func projects(ctx context.Context, adminKey string) ([]project, error) {
	return list[project](ctx, adminKey, "/organization/projects", url.Values{})
}

// ListKeys lists every project, then every API key in each project.
func (Provider) ListKeys(ctx context.Context, adminKey string) ([]core.ProviderKey, error) {
	ps, err := projects(ctx, adminKey)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var out []core.ProviderKey
	for _, p := range ps {
		// owner_project_access=any includes keys whose owner lost project
		// access; those keys still work and are exactly the forgotten ones.
		keys, err := list[apiKey](ctx, adminKey,
			"/organization/projects/"+url.PathEscape(p.ID)+"/api_keys",
			url.Values{"owner_project_access": {"any"}})
		if err != nil {
			return out, err
		}
		for _, k := range keys {
			pk := core.ProviderKey{
				Provider:  core.OpenAI,
				ID:        k.ID,
				Name:      k.Name,
				Hint:      k.RedactedValue,
				Project:   p.ID,
				Active:    k.ExpiresAt == nil || time.Unix(*k.ExpiresAt, 0).After(now),
				CreatedAt: time.Unix(k.CreatedAt, 0).UTC(),
			}
			if k.LastUsedAt != nil && *k.LastUsedAt > 0 {
				pk.LastUsed = time.Unix(*k.LastUsedAt, 0).UTC()
			}
			out = append(out, pk)
		}
	}
	return out, nil
}

type usagePage[T any] struct {
	Data []struct {
		StartTime int64 `json:"start_time"`
		Results   []T   `json:"results"`
	} `json:"data"`
	HasMore  bool    `json:"has_more"`
	NextPage *string `json:"next_page"`
}

type costResult struct {
	Amount struct {
		Value float64 `json:"value"`
	} `json:"amount"`
	APIKeyID *string `json:"api_key_id"`
}

type completionResult struct {
	InputTokens       int64   `json:"input_tokens"`
	InputCachedTokens int64   `json:"input_cached_tokens"`
	CacheWrite        int64   `json:"input_cache_write_tokens"`
	CacheWrite30m     int64   `json:"input_cache_write_30m_tokens"`
	CacheWrite12h     int64   `json:"input_cache_write_12h_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	APIKeyID          *string `json:"api_key_id"`
	Model             *string `json:"model"`
	Batch             *bool   `json:"batch"`
}

// pages follows page/next_page pagination for usage and cost endpoints.
func pages[T any](ctx context.Context, adminKey, path string, q url.Values, each func(start time.Time, r T)) error {
	for {
		var page usagePage[T]
		if err := call(ctx, adminKey, http.MethodGet, path, q, nil, &page); err != nil {
			return err
		}
		for _, b := range page.Data {
			start := time.Unix(b.StartTime, 0).UTC()
			for _, r := range b.Results {
				each(start, r)
			}
		}
		if !page.HasMore || page.NextPage == nil || *page.NextPage == "" {
			return nil
		}
		q.Set("page", *page.NextPage)
	}
}

type bucketKey struct {
	key   string
	start time.Time
}

// Usage returns spend per key. "1d" uses the costs API (exact USD). "1h"
// uses completions usage × the bundled price table (Approx).
func (Provider) Usage(ctx context.Context, adminKey string, from, to time.Time, bucket string) ([]core.UsagePoint, error) {
	sum := map[bucketKey]float64{}
	var approx bool
	switch bucket {
	case "1d":
		// The costs API accepts up to 180 buckets per page.
		for _, w := range windows(from, to, 24*time.Hour, 180) {
			q := url.Values{
				"start_time":   {strconv.FormatInt(w[0].Unix(), 10)},
				"end_time":     {strconv.FormatInt(w[1].Unix(), 10)},
				"bucket_width": {"1d"},
				"group_by[]":   {"api_key_id"},
				"limit":        {"180"},
			}
			err := pages(ctx, adminKey, "/organization/costs", q, func(start time.Time, r costResult) {
				if r.APIKeyID != nil && *r.APIKeyID != "" {
					sum[bucketKey{*r.APIKeyID, start}] += r.Amount.Value
				}
			})
			if err != nil {
				return points(sum, false), err
			}
		}
	case "1h":
		approx = true
		// Completions usage allows at most 168 hourly buckets per request.
		for _, w := range windows(from, to, time.Hour, 168) {
			q := url.Values{
				"start_time":   {strconv.FormatInt(w[0].Unix(), 10)},
				"end_time":     {strconv.FormatInt(w[1].Unix(), 10)},
				"bucket_width": {"1h"},
				"group_by[]":   {"api_key_id", "model", "batch"},
				"limit":        {"168"},
			}
			err := pages(ctx, adminKey, "/organization/usage/completions", q, func(start time.Time, r completionResult) {
				if r.APIKeyID == nil || *r.APIKeyID == "" {
					return
				}
				model := ""
				if r.Model != nil {
					model = *r.Model
				}
				w30, w12 := r.CacheWrite30m, r.CacheWrite12h
				if w30+w12 == 0 {
					w30 = r.CacheWrite
				}
				batch := r.Batch != nil && *r.Batch
				sum[bucketKey{*r.APIKeyID, start}] += tokenCost(model, r.InputTokens, r.InputCachedTokens, w30, w12, r.OutputTokens, batch)
			})
			if err != nil {
				return points(sum, true), err
			}
		}
	default:
		return nil, fmt.Errorf("openai: unsupported bucket %q (use 1d or 1h)", bucket)
	}
	return points(sum, approx), nil
}

func points(sum map[bucketKey]float64, approx bool) []core.UsagePoint {
	out := make([]core.UsagePoint, 0, len(sum))
	for k, v := range sum {
		out = append(out, core.UsagePoint{KeyID: k.key, Start: k.start, USD: v, Approx: approx})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].KeyID < out[j].KeyID
	})
	return out
}

// Uncapped returns project ids with no hard spend limit. If the organization
// itself has a hard limit, every project is capped by it and none is returned.
// A 404 from a spend_limit endpoint means no limit is set.
func (Provider) Uncapped(ctx context.Context, adminKey string) ([]string, error) {
	err := call(ctx, adminKey, http.MethodGet, "/organization/spend_limit", nil, nil, nil)
	if err == nil {
		return nil, nil
	}
	if !isNotFound(err) {
		return nil, err
	}
	ps, err := projects(ctx, adminKey)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range ps {
		err := call(ctx, adminKey, http.MethodGet, "/organization/projects/"+url.PathEscape(p.ID)+"/spend_limit", nil, nil, nil)
		switch {
		case err == nil:
		case isNotFound(err):
			out = append(out, p.ID)
		default:
			return out, err
		}
	}
	return out, nil
}

func isNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// Disable deletes the key. This cannot be undone.
func (Provider) Disable(ctx context.Context, adminKey string, k core.ProviderKey) error {
	if k.Project == "" || k.ID == "" {
		return fmt.Errorf("openai: key is missing its project or id")
	}
	err := call(ctx, adminKey, http.MethodDelete,
		"/organization/projects/"+url.PathEscape(k.Project)+"/api_keys/"+url.PathEscape(k.ID), nil, nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
		return fmt.Errorf("OpenAI refused to delete this key (HTTP 403: %s). Your admin key is probably read-only; delete the key in the OpenAI dashboard, or connect an admin key with write access", ae.Message)
	}
	return err
}
