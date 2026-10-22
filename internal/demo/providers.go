package demo

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

const day = 24 * time.Hour

// fakeKey is one provider key plus how it spends.
type fakeKey struct {
	core.ProviderKey
	lo, hi float64 // normal daily spend range in USD
	spike  float64 // USD spent in the most recent hour; 0 for none
}

type fakeProvider struct {
	name, adminURL string
	reversible     bool
	approx         bool // Anthropic cost is tokens × price, shown with ≈
	keys           []fakeKey
	uncapped       []string
}

func providers() []provider.Provider {
	t := now().UTC()
	ago := func(days int) time.Time { return t.AddDate(0, 0, -days) }
	// Hints are what the admin APIs return. Built at runtime like testutil's
	// keys; none of them is a full key.
	openAIHint := func(last4 string) string { return "sk-" + "proj-key..." + last4 }

	openai := &fakeProvider{
		name:     core.OpenAI,
		adminURL: "https://platform.openai.com/settings/organization/admin-keys",
		uncapped: []string{"prod"},
		keys: []fakeKey{
			{ProviderKey: core.ProviderKey{
				ID: "key_demo_a91f", Name: "support-bot", Hint: testutil.OpenAI1Hint,
				CreatedAt: ago(143), LastUsed: t.Add(-2 * time.Minute),
			}, lo: 2.10, hi: 3.90, spike: 41.20},
			{ProviderKey: core.ProviderKey{
				ID: "key_demo_3e0b", Name: "email-agent", Hint: openAIHint(core.Last4(testutil.OpenAI2)),
				CreatedAt: ago(38), LastUsed: t.Add(-17 * time.Minute),
			}, lo: 0.60, hi: 1.40},
			{ProviderKey: core.ProviderKey{
				ID: "key_demo_9c41", Name: "old-laptop", Hint: openAIHint("9c41"),
				CreatedAt: ago(211), LastUsed: t.Add(-5 * time.Hour),
			}, lo: 0.15, hi: 0.45},
		},
	}
	anthropic := &fakeProvider{
		name:       core.Anthropic,
		adminURL:   "https://console.anthropic.com/settings/admin-keys",
		reversible: true,
		approx:     true,
		keys: []fakeKey{
			{ProviderKey: core.ProviderKey{
				ID: "apikey_demo_77c2", Name: "landing-chat", Hint: testutil.Anthropic1Hint,
				CreatedAt: ago(61), LastUsed: t.Add(-9 * time.Minute),
			}, lo: 1.10, hi: 2.30},
		},
	}
	for _, p := range []*fakeProvider{openai, anthropic} {
		for i := range p.keys {
			k := &p.keys[i].ProviderKey
			k.Provider, k.Project, k.Active = p.name, "prod", true
		}
	}
	return []provider.Provider{openai, anthropic}
}

func (p *fakeProvider) Name() string        { return p.name }
func (p *fakeProvider) AdminKeyURL() string { return p.adminURL }
func (p *fakeProvider) Reversible() bool    { return p.reversible }

func (p *fakeProvider) Connect(context.Context, string) error {
	fmt.Println("  (demo) No admin key needed. Using built-in demo data.")
	return nil
}

func (p *fakeProvider) ListKeys(context.Context, string) ([]core.ProviderKey, error) {
	out := make([]core.ProviderKey, len(p.keys))
	for i, k := range p.keys {
		out[i] = k.ProviderKey
	}
	return out, nil
}

func (p *fakeProvider) Uncapped(context.Context, string) ([]string, error) {
	return p.uncapped, nil
}

func (p *fakeProvider) Disable(_ context.Context, _ string, k core.ProviderKey) error {
	fmt.Printf("  (demo) Would disable %s (…%s). Nothing was sent.\n", k.Name, core.Last4(k.Hint))
	return nil
}

// Usage returns spend per key per bucket in [from, to). Daily buckets wander
// inside each key's normal range. Hourly buckets spread that over the day,
// except the most recent bucket, which carries the spike if the key has one.
// Today's daily bucket only counts the hours elapsed so far, plus the spike.
func (p *fakeProvider) Usage(_ context.Context, _ string, from, to time.Time, bucket string) ([]core.UsagePoint, error) {
	var step time.Duration
	switch bucket {
	case "1d":
		step = day
	case "1h":
		step = time.Hour
	default:
		return nil, fmt.Errorf("demo: unsupported bucket %q", bucket)
	}
	t := now().UTC()
	var starts []time.Time
	for s := from.UTC().Truncate(step); s.Before(to) && !s.After(t); s = s.Add(step) {
		starts = append(starts, s)
	}

	var out []core.UsagePoint
	for _, k := range p.keys {
		for i, s := range starts {
			if s.Add(step).Before(k.CreatedAt) {
				continue
			}
			normal := k.lo + (k.hi-k.lo)*noise(k.ID, s.Truncate(day))
			var usd float64
			if step == day {
				usd = normal
				if s.Equal(t.Truncate(day)) {
					usd = normal*t.Sub(s).Hours()/24 + k.spike
				}
			} else {
				usd = normal / 24 * hourShape(s.Hour())
				// The newest bucket is "the last hour", whichever way the
				// caller cuts it (current partial hour or last full hour).
				if k.spike > 0 && i == len(starts)-1 && t.Sub(s) < 2*time.Hour {
					usd = k.spike
				}
			}
			out = append(out, core.UsagePoint{KeyID: k.ID, Start: s, USD: cents(usd), Approx: p.approx})
		}
	}
	return out, nil
}

// noise is a stable value in [0, 1) for a key and a day, so numbers don't
// jump around between runs on the same day.
func noise(id string, d time.Time) float64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%d", id, d.Unix())
	return float64(h.Sum64()%10000) / 10000
}

// hourShape is busier during the (UTC) working day; it averages to 1.
func hourShape(hour int) float64 {
	return 1 + 0.6*math.Sin(float64(hour-9)*math.Pi/12)
}

func cents(v float64) float64 { return math.Round(v*100) / 100 }
