// Package demo swaps every source and provider for fakes built from
// obviously fake keys, so `keywatch --demo` works offline for screenshots.
//
// The story is a small startup with three AI features:
//
//   - support-bot (Railway) uses OpenAI key …a91f. The same key is committed
//     in .env.local and sits in a GitHub Actions secret. In the last hour it
//     spent about $41 against a normal day of about $3.
//   - email-agent (Railway) uses OpenAI key …3e0b.
//   - landing-chat (Vercel) uses Anthropic key …77c2.
//   - OpenAI also has "old-laptop" (…9c41): active, still spending a little,
//     and found nowhere.
//
// Nothing here touches the network or the keychain.
package demo

import (
	"os"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/creds"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/source"
)

// adminKey is what demo mode reports as the stored admin key, so inventory
// treats both providers as connected. It is not key-shaped on purpose.
const adminKey = "demo"

// now is overridable in tests.
var now = time.Now

// Enable replaces the source and provider registries with demo fakes.
func Enable() {
	source.Reset(sources()...)
	provider.Reset(providers()...)
	// inventory asks creds whether a provider is connected. Env vars win over
	// the keychain, so this also keeps demo mode from reading real keys.
	for _, p := range []string{core.OpenAI, core.Anthropic} {
		os.Setenv(creds.EnvVar(p), adminKey)
	}
}
