// Package inventory is the one pipeline every command uses: read sources,
// read providers, join them into keys, attach spend.
package inventory

import (
	"context"
	"sort"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/creds"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/source"
)

type Options struct {
	// UseSource decides whether a source that is Available gets read.
	// Local is always read. nil means read everything available.
	UseSource func(source.Source) bool
	// Spend pulls 14 days of daily usage when a provider is connected.
	Spend bool
	Now   time.Time
}

type Result struct {
	Keys      []core.Key
	Notes     []string
	ReadFrom  []string // for the receipt: every host contacted, "x (failed)" if it errored
	Connected []string // providers with an admin key
	Uncapped  map[string][]string
	Errors    []error // non-fatal: one source failing never stops the run
}

func Load(ctx context.Context, o Options) Result {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	var r Result
	r.Uncapped = map[string][]string{}
	var found []core.FoundSecret
	for _, s := range source.All() {
		if !s.Available() {
			continue
		}
		if s.Name() != "local" && o.UseSource != nil && !o.UseSource(s) {
			continue
		}
		sec, notes, err := s.Collect(ctx)
		if err != nil {
			r.Errors = append(r.Errors, err)
			if s.Name() != "local" {
				r.ReadFrom = append(r.ReadFrom, s.Name()+" (failed)")
			}
			continue
		}
		if s.Name() != "local" {
			r.ReadFrom = append(r.ReadFrom, s.Name())
		}
		found = append(found, sec...)
		r.Notes = append(r.Notes, notes...)
	}

	var remote []core.ProviderKey
	usage := map[string][]core.UsagePoint{}
	for _, p := range provider.All() {
		ak := creds.Get(p.Name())
		if ak == "" {
			continue
		}
		r.Connected = append(r.Connected, p.Name())
		keys, err := p.ListKeys(ctx, ak)
		if err != nil {
			r.Errors = append(r.Errors, err)
			r.ReadFrom = append(r.ReadFrom, p.Name()+" (failed)")
			continue
		}
		r.ReadFrom = append(r.ReadFrom, p.Name())
		remote = append(remote, keys...)
		if u, err := p.Uncapped(ctx, ak); err == nil && len(u) > 0 {
			r.Uncapped[p.Name()] = u
		}
		if o.Spend {
			day := o.Now.UTC().Truncate(24 * time.Hour)
			pts, err := p.Usage(ctx, ak, day.AddDate(0, 0, -14), day.AddDate(0, 0, 1), "1d")
			if err != nil {
				r.Errors = append(r.Errors, err)
			}
			for _, pt := range pts {
				usage[pt.KeyID] = append(usage[pt.KeyID], pt)
			}
		}
	}

	r.Keys = core.Join(found, remote)
	if o.Spend {
		for i := range r.Keys {
			if k := r.Keys[i]; k.Remote != nil {
				r.Keys[i].Spend = summarize(usage[k.Remote.ID], o.Now)
			}
		}
	}
	return r
}

// summarize turns daily points into today, last 7 days and a normal day
// (median of the 14 days before today, counting days with no spend as 0).
func summarize(pts []core.UsagePoint, now time.Time) *core.Spend {
	s := &core.Spend{}
	today := now.UTC().Truncate(24 * time.Hour)
	daily := map[time.Time]float64{}
	for _, p := range pts {
		d := p.Start.UTC().Truncate(24 * time.Hour)
		daily[d] += p.USD
		s.Approx = s.Approx || p.Approx
	}
	s.Today = daily[today]
	var hist []float64
	for i := 1; i <= 14; i++ {
		d := today.AddDate(0, 0, -i)
		hist = append(hist, daily[d])
		if i < 7 {
			s.Last7d += daily[d]
		}
	}
	s.Last7d += s.Today
	sort.Float64s(hist)
	s.DailyP50 = (hist[6] + hist[7]) / 2
	return s
}
