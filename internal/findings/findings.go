// Package findings turns an inventory into a short, ranked list of things
// worth fixing, each with a next step.
package findings

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/inventory"
)

var limitURL = map[string]string{
	core.OpenAI:    "platform.openai.com/settings/organization/limits",
	core.Anthropic: "platform.claude.com/settings/limits",
}

// From computes findings, drops ignored ones, and sorts by severity.
func From(r inventory.Result, cfg *config.Config, now time.Time) []core.Finding {
	var out []core.Finding
	add := func(f core.Finding) {
		if cfg == nil || !cfg.Ignored(f.Code, f.KeyID) {
			out = append(out, f)
		}
	}

	for _, k := range r.Keys {
		if k.ID == "?" {
			continue
		}
		var committed []string
		deployed := map[string]bool{}
		for _, l := range k.Locations {
			if l.Committed {
				committed = append(committed, l.Where)
			}
			if l.Source != "local" && !l.NameOnly {
				deployed[l.Where] = true
			}
		}
		if len(committed) > 0 {
			add(core.Finding{Code: "F1", Severity: core.High, KeyID: k.ID,
				Title: fmt.Sprintf("%s is committed to git (%s)", k.Redacted, strings.Join(committed, ", ")),
				Fix:   "treat it as leaked: replace it, then keywatch disable " + k.ID})
		}
		if len(deployed) >= 2 {
			add(core.Finding{Code: "F2", Severity: core.Medium, KeyID: k.ID,
				Title: fmt.Sprintf("%d services share %s", len(deployed), k.Redacted),
				Fix:   "give each service its own key so one leak doesn't take down the rest"})
		}
		if len(k.Locations) == 0 && k.Remote != nil {
			add(core.Finding{Code: "F3", Severity: core.Medium, KeyID: k.ID,
				Title: fmt.Sprintf("%q (%s) is active but wasn't found anywhere", k.Label, k.Redacted),
				Fix:   "if nobody claims it: keywatch disable " + k.ID})
		}
		if k.Remote != nil && !k.Remote.CreatedAt.IsZero() && now.Sub(k.Remote.CreatedAt) > 90*24*time.Hour {
			add(core.Finding{Code: "F5", Severity: core.Low, KeyID: k.ID,
				Title: fmt.Sprintf("%s is %d days old", k.Redacted, int(now.Sub(k.Remote.CreatedAt).Hours()/24)),
				Fix:   "swap it for a fresh one when you next deploy"})
		}
	}
	for p, ids := range r.Uncapped {
		for _, id := range ids {
			add(core.Finding{Code: "F4", Severity: core.Medium, KeyID: id,
				Title: fmt.Sprintf("No spending cap on %s project %q", p, id),
				Fix:   "set one: " + limitURL[p]})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Severity > out[j].Severity })
	return out
}
