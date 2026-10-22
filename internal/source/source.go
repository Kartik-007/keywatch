// Package source defines where secrets are read from. Each platform lives in
// its own subpackage and registers itself in init().
package source

import (
	"context"

	"github.com/Kartik-007/keywatch/internal/core"
)

// Source reads secrets from one place.
type Source interface {
	// Name is a short id: "local", "railway", "vercel", "github".
	Name() string
	// Available reports whether credentials for this source exist on the
	// machine (e.g. an existing CLI login). It must not touch the network.
	Available() bool
	// Collect returns every AI secret it can see. Notes are plain-English
	// caveats shown to the user, e.g. "GitHub secrets are write-only:
	// matched 2 by name".
	Collect(ctx context.Context) (secrets []core.FoundSecret, notes []string, err error)
}

var registry []Source

// Register adds a source. Call from init().
func Register(s Source) { registry = append(registry, s) }

// All returns every registered source in registration order.
func All() []Source { return registry }

// Reset replaces every registered source. Used by demo mode and tests.
func Reset(s ...Source) { registry = s }
