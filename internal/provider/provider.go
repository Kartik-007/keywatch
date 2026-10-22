// Package provider defines the AI providers keywatch reads keys and spend
// from. Each provider lives in its own subpackage and registers in init().
package provider

import (
	"context"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
)

// Provider talks to one provider's admin API using an admin key.
type Provider interface {
	Name() string // core.OpenAI or core.Anthropic
	// AdminKeyURL is where the user creates an admin key.
	AdminKeyURL() string
	// Connect validates an admin key with one cheap read call.
	Connect(ctx context.Context, adminKey string) error
	ListKeys(ctx context.Context, adminKey string) ([]core.ProviderKey, error)
	// Usage returns spend per key per bucket ("1h" or "1d") in [from, to).
	Usage(ctx context.Context, adminKey string, from, to time.Time, bucket string) ([]core.UsagePoint, error)
	// Uncapped returns project/workspace ids that have no spend limit set.
	// Return nil, nil if the provider doesn't expose limits.
	Uncapped(ctx context.Context, adminKey string) ([]string, error)
	// Disable turns a key off. Reversible reports whether Enable can undo it.
	Disable(ctx context.Context, adminKey string, k core.ProviderKey) error
	Reversible() bool
}

var registry = map[string]Provider{}

func Register(p Provider) { registry[p.Name()] = p }

func Get(name string) (Provider, bool) { p, ok := registry[name]; return p, ok }

func All() []Provider {
	out := []Provider{}
	for _, n := range []string{core.OpenAI, core.Anthropic} {
		if p, ok := registry[n]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Reset replaces every registered provider. Used by demo mode and tests.
func Reset(ps ...Provider) {
	registry = map[string]Provider{}
	for _, p := range ps {
		Register(p)
	}
}
