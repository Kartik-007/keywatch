package openai

import "strings"

// price is USD per 1M tokens. Cached 0 means the model has no cached-input
// price; cached tokens are then charged at the input price.
type price struct{ In, Cached, Out float64 }

// prices are OpenAI's Standard-tier, short-context list prices from
// developers.openai.com/api/docs/pricing, checked 2026-10-21. Only used for
// hourly (1h) spend; daily spend comes from the costs API in exact USD.
//
// Approximations (all costs from this table are shown with "≈"):
//   - Long-context rates (prompts over the model's short-context threshold)
//     are not applied, so very long prompts are under-counted.
//   - Cache writes are charged at 1.25x input (30m) and 2.5x input (12h),
//     matching the published write prices.
//   - Batch usage is charged at 50%. Flex, priority and fast tiers are not
//     separated and are charged at standard rates.
//   - Dated snapshots match by longest model-name prefix, e.g.
//     "gpt-4o-mini-2024-07-18" uses "gpt-4o-mini".
var prices = map[string]price{
	"gpt-6-astra":       {10, 1, 50},
	"gpt-6.1-sol":       {2, 0.10, 10},
	"gpt-6-sol":         {2, 0.20, 10},
	"gpt-6-luna":        {0.10, 0.01, 0.50},
	"gpt-5.6-sol":       {4, 0.40, 20},
	"gpt-5.6-terra":     {2, 0.20, 12},
	"gpt-5.6-luna":      {0.20, 0.02, 1.20},
	"gpt-5.5":           {5, 0.50, 30},
	"gpt-5.5-pro":       {30, 0, 180},
	"gpt-5.4":           {2.50, 0.25, 15},
	"gpt-5.4-pro":       {30, 0, 180},
	"gpt-5.4-mini":      {0.75, 0.075, 4.50},
	"gpt-5.4-nano":      {0.20, 0.02, 1.25},
	"gpt-5.2":           {1.75, 0.175, 14},
	"gpt-5.2-pro":       {21, 0, 168},
	"gpt-5.1":           {1.25, 0.125, 10},
	"gpt-5":             {1.25, 0.125, 10},
	"gpt-5-mini":        {0.25, 0.025, 2},
	"gpt-5-nano":        {0.05, 0.005, 0.40},
	"gpt-5-pro":         {15, 0, 120},
	"gpt-4.1":           {2, 0.50, 8},
	"gpt-4.1-mini":      {0.40, 0.10, 1.60},
	"gpt-4.1-nano":      {0.10, 0.025, 0.40},
	"gpt-4o":            {2.50, 1.25, 10},
	"gpt-4o-2024-05-13": {5, 0, 15},
	"gpt-4o-mini":       {0.15, 0.075, 0.60},
	"o1":                {15, 7.50, 60},
	"o1-pro":            {150, 0, 600},
	"o3":                {2, 0.50, 8},
	"o3-pro":            {20, 0, 80},
	"o3-mini":           {1.10, 0.55, 4.40},
	"o4-mini":           {1.10, 0.275, 4.40},
	"gpt-4-turbo":       {10, 0, 30},
	"gpt-4":             {30, 0, 60},
	"gpt-3.5-turbo":     {0.50, 0, 1.50},
}

// fallback prices unknown models like the current flagship, so a new or
// unlisted model is more likely over- than under-counted.
var fallback = price{10, 1, 50}

// priceFor returns the price for a model by longest matching prefix.
func priceFor(model string) price {
	model = strings.TrimPrefix(strings.ToLower(model), "ft:")
	best, bestLen := fallback, 0
	for name, p := range prices {
		if strings.HasPrefix(model, name) && len(name) > bestLen {
			best, bestLen = p, len(name)
		}
	}
	return best
}

// tokenCost is the approximate USD cost of one usage result.
func tokenCost(model string, in, cached, write30m, write12h, out int64, batch bool) float64 {
	p := priceFor(model)
	cachedPrice := p.Cached
	if cachedPrice == 0 {
		cachedPrice = p.In
	}
	uncached := in - cached - write30m - write12h
	if uncached < 0 {
		uncached = 0
	}
	usd := (float64(uncached)*p.In +
		float64(cached)*cachedPrice +
		float64(write30m)*p.In*1.25 +
		float64(write12h)*p.In*2.5 +
		float64(out)*p.Out) / 1e6
	if batch {
		usd *= 0.5
	}
	return usd
}
