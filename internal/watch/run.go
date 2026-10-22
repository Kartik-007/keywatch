package watch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/alert"
	"github.com/Kartik-007/keywatch/internal/anomaly"
	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/creds"
	"github.com/Kartik-007/keywatch/internal/inventory"
	"github.com/Kartik-007/keywatch/internal/provider"
)

// Options is what one hourly run needs. Everything comes from flags and the
// environment, so the job keeps no state between runs.
type Options struct {
	Now         time.Time
	ScaryDayUSD float64           // 0 turns the org-wide check off
	Vars        map[string]string // repo variables; acks are KEYWATCH_ACK_<ID>=<RFC3339>
	Webhook     string            // optional Slack or Discord webhook URL
	Dir         string            // where .keywatch.toml lives; "" means "."
	Out         io.Writer         // nil means stdout
}

// ErrAlerts is returned (wrapped) when any alert fired, so the Action fails
// and GitHub emails the user.
var ErrAlerts = errors.New("spend looks wrong")

// Run is what the hourly Action executes: read 15 days of hourly usage from
// every connected provider, check it, print alerts, post them to the webhook
// if one is set, and return an error if anything fired. It writes no files.
func Run(ctx context.Context, o Options) error {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Dir == "" {
		o.Dir = "."
	}
	now := o.Now.UTC()
	out := o.Out

	// Locations and labels are a bonus: in CI most platform logins don't
	// exist, so keys fall back to the provider's names for them.
	inv := inventory.Load(ctx, inventory.Options{Now: now})
	for _, err := range inv.Errors {
		fmt.Fprintf(out, "note: %v\n", err)
	}
	if len(inv.Connected) == 0 {
		var names []string
		for _, p := range provider.All() {
			names = append(names, creds.EnvVar(p.Name()))
		}
		return fmt.Errorf("no provider admin keys found; set %s as repo secrets (keywatch watch does this)", strings.Join(names, " or "))
	}
	labels := map[string]string{}
	if cfg, err := config.Load(o.Dir); err == nil {
		labels = cfg.Labels
	}

	byID := map[string]core.Key{}
	byRemote := map[string]string{} // provider + remote id -> short id
	for _, k := range inv.Keys {
		if l, ok := labels[k.ID]; ok && l != "" {
			k.Label = l
		}
		byID[k.ID] = k
		if k.Remote != nil {
			byRemote[k.Provider+"\x00"+k.Remote.ID] = k.ID
		}
	}

	today := now.Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -(anomaly.HistoryDays + 1))
	to := now.Truncate(time.Hour).Add(time.Hour)
	var pts []core.UsagePoint
	var failed []string
	for _, p := range provider.All() {
		ak := creds.Get(p.Name())
		if ak == "" {
			continue
		}
		up, err := p.Usage(ctx, ak, from, to, "1h")
		if err != nil {
			fmt.Fprintf(out, "couldn't read %s usage: %v\n", p.Name(), err)
			failed = append(failed, p.Name())
			continue
		}
		for _, pt := range up {
			if id, ok := byRemote[p.Name()+"\x00"+pt.KeyID]; ok {
				pt.KeyID = id
			} else if _, ok := byID[pt.KeyID]; !ok {
				byID[pt.KeyID] = core.Key{ID: pt.KeyID, Label: pt.KeyID, Provider: p.Name()}
			}
			pts = append(pts, pt)
		}
	}

	acked := map[string]time.Time{}
	for _, id := range append(keyIDs(pts), anomaly.Org) {
		v, ok := o.Vars[AckVar(id)]
		if !ok {
			continue
		}
		until, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
		if err != nil {
			fmt.Fprintf(out, "note: ignoring %s: %q isn't an RFC3339 time\n", AckVar(id), v)
			continue
		}
		acked[id] = until
		if until.After(now) {
			fmt.Fprintf(out, "%s is acked until %s\n", id, until.UTC().Format("2006-01-02 15:04 UTC"))
		}
	}

	alerts := anomaly.Check(pts, now, o.ScaryDayUSD, acked)
	var texts []string
	for _, a := range alerts {
		id := a.KeyID
		if a.Reason == anomaly.ScaryDay {
			id = a.TopKeyID
		}
		texts = append(texts, alert.Render(a, byID[id]))
	}

	var errs []error
	if len(alerts) == 0 {
		fmt.Fprintf(out, "All quiet. Checked %d keys from %s at %s; spent %s today.\n",
			len(keyIDs(pts)), strings.Join(inv.Connected, ", "), now.Format("15:04 UTC"), todayTotal(pts, today))
	} else {
		fmt.Fprintln(out, strings.Join(texts, "\n---\n\n"))
		if o.Webhook != "" {
			if err := alert.Post(ctx, o.Webhook, strings.Join(texts, "\n")); err != nil {
				fmt.Fprintf(out, "couldn't post to the webhook: %v\n", err)
				errs = append(errs, err)
			}
		}
		errs = append(errs, fmt.Errorf("%w: %d alert(s), see above", ErrAlerts, len(alerts)))
	}
	if len(failed) > 0 {
		errs = append(errs, fmt.Errorf("couldn't read usage from %s, so those keys weren't checked", strings.Join(failed, ", ")))
	}
	return errors.Join(errs...)
}

func keyIDs(pts []core.UsagePoint) []string {
	seen := map[string]bool{}
	var ids []string
	for _, p := range pts {
		if !seen[p.KeyID] {
			seen[p.KeyID] = true
			ids = append(ids, p.KeyID)
		}
	}
	sort.Strings(ids)
	return ids
}

func todayTotal(pts []core.UsagePoint, today time.Time) string {
	sum, approx := 0.0, false
	for _, p := range pts {
		if !p.Start.Before(today) {
			sum += p.USD
			approx = approx || p.Approx
		}
	}
	s := fmt.Sprintf("$%.2f", sum)
	if approx {
		s = "≈" + s
	}
	return s
}
