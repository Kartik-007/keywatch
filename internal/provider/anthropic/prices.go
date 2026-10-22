package anthropic

import "strings"

// price is USD per 1M tokens.
type price struct{ In, CacheRead, Out float64 }

// prices are Claude API list prices from
// platform.claude.com/docs/en/about-claude/pricing, checked 2026-10-21.
//
// Approximations (all Anthropic costs are shown with "≈"):
//   - Cache writes: 1.25x input (5m) and 2x input (1h), per the pricing page.
//   - Batch (service_tier "batch"): 50% off. Other tiers (flex, priority)
//     are charged at standard rates.
//   - Fast mode (speed "fast"): 2x, which matches the published fast prices
//     for Opus 5.5, Opus 5 and Opus 4.8.
//   - Not applied: US-only inference_geo (1.1x) and Haiku 5.5's higher price
//     for prompts over 100k tokens, so those are slightly under-counted.
//   - Web search: $10 per 1,000 requests.
//   - Dated ids match by longest prefix, e.g. "claude-sonnet-4-5-20250929"
//     uses "claude-sonnet-4-5".
var prices = map[string]price{
	"claude-fable-5-1":  {10, 0.25, 50},
	"claude-mythos-5-1": {10, 0.25, 50},
	"claude-fable-5":    {10, 1, 50},
	"claude-mythos-5":   {10, 1, 50},
	"claude-opus-5-5":   {4, 0.20, 20},
	"claude-opus-5":     {5, 0.50, 25},
	"claude-opus-4-8":   {5, 0.50, 25},
	"claude-opus-4-7":   {5, 0.50, 25},
	"claude-opus-4-6":   {5, 0.50, 25},
	"claude-opus-4-5":   {5, 0.50, 25},
	"claude-opus-4-1":   {15, 1.50, 75},
	"claude-opus-4":     {15, 1.50, 75},
	"claude-sonnet-5-5": {2, 0.10, 10},
	"claude-sonnet-5":   {2, 0.20, 10},
	"claude-sonnet-4-6": {3, 0.30, 15},
	"claude-sonnet-4-5": {3, 0.30, 15},
	"claude-sonnet-4":   {3, 0.30, 15},
	"claude-haiku-5-5":  {0.10, 0.01, 0.50},
	"claude-haiku-4-5":  {1, 0.10, 5},
	"claude-3-5-haiku":  {0.80, 0.08, 4},
}

// fallback prices unknown models like the most expensive current model, so a
// new or unlisted model is more likely over- than under-counted.
var fallback = price{10, 1, 50}

const webSearchUSD = 10.0 / 1000

// priceFor returns the price for a model by longest matching prefix.
func priceFor(model string) price {
	model = strings.ToLower(model)
	best, bestLen := fallback, 0
	for name, p := range prices {
		if strings.HasPrefix(model, name) && len(name) > bestLen {
			best, bestLen = p, len(name)
		}
	}
	return best
}

type usage struct {
	Uncached, Write5m, Write1h, CacheRead, Out, WebSearches int64
	Model, Tier, Speed                                      string
}

// cost is the approximate USD cost of one usage result.
func cost(u usage) float64 {
	p := priceFor(u.Model)
	usd := (float64(u.Uncached)*p.In +
		float64(u.Write5m)*p.In*1.25 +
		float64(u.Write1h)*p.In*2 +
		float64(u.CacheRead)*p.CacheRead +
		float64(u.Out)*p.Out) / 1e6
	if u.Speed == "fast" {
		usd *= 2
	}
	if u.Tier == "batch" {
		usd *= 0.5
	}
	return usd + float64(u.WebSearches)*webSearchUSD
}
