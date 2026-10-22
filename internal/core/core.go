// Package core holds the types every other package shares: secrets found in
// places, keys as providers know them, and the joined view of both.
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Provider names. Keep in sync with provider packages.
const (
	OpenAI    = "openai"
	Anthropic = "anthropic"
)

// Location is one place a secret was found.
type Location struct {
	Source string // "local", "railway", "vercel", "github"
	Where  string // human label, e.g. "support-bot · production" or ".env.local"
	Var    string // variable name if known, e.g. "OPENAI_API_KEY"
	// Committed is true when a local secret is tracked by git (tree or history).
	Committed bool
	// NameOnly is true when the value couldn't be read (GitHub secrets,
	// Vercel sensitive vars) and the location was matched by variable name.
	NameOnly bool
}

// FoundSecret is a secret value seen in one location. The raw value never
// leaves this struct: render with Redact, compare with Fingerprint.
type FoundSecret struct {
	Provider string
	Value    string
	Location Location
}

func (f FoundSecret) Fingerprint() string { return Fingerprint(f.Value) }

// ProviderKey is a key as the provider's admin API describes it.
type ProviderKey struct {
	Provider  string
	ID        string // provider key id, e.g. "apikey_01..." or "key_abc"
	Name      string
	Hint      string // redacted_value / partial_key_hint, e.g. "sk-proj-abc...wxyz"
	Project   string // OpenAI project id or Anthropic workspace id
	Active    bool
	CreatedAt time.Time
	LastUsed  time.Time // zero if unknown
}

// Key is the joined view: one row per real key.
type Key struct {
	ID        string // short id shown to users: last 4 chars, e.g. "a91f"
	Label     string // derived from first deployed location, user-renamable
	Provider  string
	Redacted  string // e.g. "sk-…a91f"
	Locations []Location
	Remote    *ProviderKey // nil if not connected or not matched
	Spend     *Spend       // nil if not connected
}

// Spend is what a key cost. Approx is true when computed from tokens × price.
type Spend struct {
	Today, Last7d float64
	DailyP50      float64 // normal day
	Approx        bool
}

// UsagePoint is one bucket of spend for one key.
type UsagePoint struct {
	KeyID  string // provider key id
	Start  time.Time
	USD    float64
	Approx bool
}

type Severity int

const (
	Low Severity = iota
	Medium
	High
)

func (s Severity) String() string { return [...]string{"low", "medium", "high"}[s] }

// Finding is one thing worth fixing.
type Finding struct {
	Code     string // "F1".."F5"
	Severity Severity
	KeyID    string
	Title    string // plain-English one-liner
	Fix      string // next command, e.g. "keywatch fix 1"
}

// Fingerprint is a stable local id for a secret value. Never sent anywhere.
func Fingerprint(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:8])
}

// Redact shows the recognizable prefix and the last 4 characters only.
func Redact(secret string) string {
	if len(secret) < 12 {
		return "…"
	}
	prefix := "sk-"
	if strings.HasPrefix(secret, "sk-ant-") {
		prefix = "sk-ant-"
	}
	return prefix + "…" + Last4(secret)
}

func Last4(s string) string {
	if len(s) < 4 {
		return s
	}
	return s[len(s)-4:]
}

// MatchesHint reports whether a secret matches a provider's redacted hint,
// e.g. "sk-proj-abc...wxyz" or "sk-ant-api03-R2D...igAA".
func MatchesHint(secret, hint string) bool {
	for _, sep := range []string{"...", "…", "*"} {
		if i := strings.Index(hint, sep); i > 0 {
			pre := hint[:i]
			suf := strings.TrimLeft(hint[i:], ".…*")
			return strings.HasPrefix(secret, pre) && strings.HasSuffix(secret, suf)
		}
	}
	return false
}
