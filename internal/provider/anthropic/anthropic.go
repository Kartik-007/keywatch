package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
)

func init() { provider.Register(Provider{}) }

// Provider implements provider.Provider for Anthropic.
type Provider struct{}

func (Provider) Name() string { return core.Anthropic }

func (Provider) AdminKeyURL() string { return "https://platform.claude.com/settings/admin-keys" }

// Reversible is true: a key set to inactive can be set back to active.
func (Provider) Reversible() bool { return true }

// Connect reads the organization the key belongs to.
func (Provider) Connect(ctx context.Context, adminKey string) error {
	err := call(ctx, adminKey, http.MethodGet, "/organizations/me", nil, nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 {
		return fmt.Errorf("Anthropic rejected this key (HTTP %d: %s). keywatch needs an Admin key, and the Admin API only works for organization accounts, not individual ones. An organization admin can create one at %s (set up an organization in Console settings first if you don't have one)",
			ae.Status, ae.Message, Provider{}.AdminKeyURL())
	}
	return err
}

type apiKey struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	CreatedAt      string  `json:"created_at"`
	PartialKeyHint *string `json:"partial_key_hint"`
	Status         string  `json:"status"`
	WorkspaceID    *string `json:"workspace_id"`
	Scope          struct {
		Type        string `json:"type"`
		WorkspaceID string `json:"workspace_id"`
	} `json:"scope"`
}

type keyPage struct {
	Data    []apiKey `json:"data"`
	HasMore bool     `json:"has_more"`
	LastID  *string  `json:"last_id"`
}

// ListKeys lists every API key in the organization, all statuses.
func (Provider) ListKeys(ctx context.Context, adminKey string) ([]core.ProviderKey, error) {
	var out []core.ProviderKey
	q := url.Values{"limit": {"1000"}}
	for {
		var page keyPage
		if err := call(ctx, adminKey, http.MethodGet, "/organizations/api_keys", q, nil, &page); err != nil {
			return out, err
		}
		for _, k := range page.Data {
			out = append(out, toProviderKey(k))
		}
		if !page.HasMore || page.LastID == nil || *page.LastID == "" {
			return out, nil
		}
		q.Set("after_id", *page.LastID)
	}
}

func toProviderKey(k apiKey) core.ProviderKey {
	pk := core.ProviderKey{
		Provider: core.Anthropic,
		ID:       k.ID,
		Name:     k.Name,
		Active:   k.Status == "active",
		Project:  k.Scope.WorkspaceID,
	}
	if pk.Project == "" && k.WorkspaceID != nil {
		pk.Project = *k.WorkspaceID
	}
	if k.PartialKeyHint != nil {
		pk.Hint = *k.PartialKeyHint
	}
	if t, err := time.Parse(time.RFC3339Nano, k.CreatedAt); err == nil {
		pk.CreatedAt = t.UTC()
	}
	return pk
}

type usageResult struct {
	APIKeyID      *string `json:"api_key_id"`
	Model         *string `json:"model"`
	ServiceTier   *string `json:"service_tier"`
	Speed         *string `json:"speed"`
	Uncached      int64   `json:"uncached_input_tokens"`
	CacheRead     int64   `json:"cache_read_input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheCreation struct {
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
	ServerToolUse struct {
		WebSearchRequests int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
}

type usagePage struct {
	Data []struct {
		StartingAt string        `json:"starting_at"`
		Results    []usageResult `json:"results"`
	} `json:"data"`
	HasMore  bool    `json:"has_more"`
	NextPage *string `json:"next_page"`
}

type bucketKey struct {
	key   string
	start time.Time
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Usage returns approximate spend per key from the messages usage report,
// grouped by key, model, service tier and speed, priced with the bundled table.
func (Provider) Usage(ctx context.Context, adminKey string, from, to time.Time, bucket string) ([]core.UsagePoint, error) {
	var step time.Duration
	var max int
	switch bucket {
	case "1d":
		step, max = 24*time.Hour, 31
	case "1h":
		step, max = time.Hour, 168
	default:
		return nil, fmt.Errorf("anthropic: unsupported bucket %q (use 1d or 1h)", bucket)
	}
	sum := map[bucketKey]float64{}
	for _, w := range windows(from.UTC(), to.UTC(), step, max) {
		q := url.Values{
			"starting_at":  {w[0].Format(time.RFC3339)},
			"ending_at":    {w[1].Format(time.RFC3339)},
			"bucket_width": {bucket},
			"group_by[]":   {"api_key_id", "model", "service_tier", "speed"},
			"limit":        {fmt.Sprint(max)},
		}
		for {
			var page usagePage
			if err := call(ctx, adminKey, http.MethodGet, "/organizations/usage_report/messages", q, nil, &page); err != nil {
				return points(sum), err
			}
			for _, b := range page.Data {
				start, err := time.Parse(time.RFC3339, b.StartingAt)
				if err != nil {
					continue
				}
				for _, r := range b.Results {
					id := str(r.APIKeyID)
					if id == "" { // Console (workbench) usage has no key
						continue
					}
					sum[bucketKey{id, start.UTC()}] += cost(usage{
						Uncached:    r.Uncached,
						Write5m:     r.CacheCreation.Ephemeral5m,
						Write1h:     r.CacheCreation.Ephemeral1h,
						CacheRead:   r.CacheRead,
						Out:         r.OutputTokens,
						WebSearches: r.ServerToolUse.WebSearchRequests,
						Model:       str(r.Model),
						Tier:        str(r.ServiceTier),
						Speed:       str(r.Speed),
					})
				}
			}
			if !page.HasMore || page.NextPage == nil || *page.NextPage == "" {
				break
			}
			q.Set("page", *page.NextPage)
		}
	}
	return points(sum), nil
}

func points(sum map[bucketKey]float64) []core.UsagePoint {
	out := make([]core.UsagePoint, 0, len(sum))
	for k, v := range sum {
		out = append(out, core.UsagePoint{KeyID: k.key, Start: k.start, USD: v, Approx: true})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].KeyID < out[j].KeyID
	})
	return out
}

// Uncapped returns nil, nil: the Admin API doesn't expose workspace spend
// limits for Console organizations (see package doc).
func (Provider) Uncapped(ctx context.Context, adminKey string) ([]string, error) { return nil, nil }

// Disable sets the key to inactive. Enable undoes it.
func (Provider) Disable(ctx context.Context, adminKey string, k core.ProviderKey) error {
	return setStatus(ctx, adminKey, k, "inactive")
}

// Enable sets a key back to active. Used by `keywatch enable`.
func Enable(ctx context.Context, adminKey string, k core.ProviderKey) error {
	return setStatus(ctx, adminKey, k, "active")
}

func setStatus(ctx context.Context, adminKey string, k core.ProviderKey, status string) error {
	if k.ID == "" {
		return fmt.Errorf("anthropic: key is missing its id")
	}
	var got apiKey
	err := call(ctx, adminKey, http.MethodPost, "/organizations/api_keys/"+url.PathEscape(k.ID),
		nil, map[string]string{"status": status}, &got)
	if err != nil {
		return err
	}
	if got.Status != "" && got.Status != status {
		return fmt.Errorf("anthropic: asked for status %q but the key is %q", status, got.Status)
	}
	return nil
}
