// Package anomaly decides when spend looks wrong. It is pure: hourly usage in,
// alerts out, so the hourly GitHub Action can stay stateless.
package anomaly

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
)

const (
	// MinHourUSD is the floor under the spike threshold, so a key that
	// normally spends nothing doesn't alert on a few cents.
	MinHourUSD = 5.0
	// Multiplier times a key's normal hour is a spike.
	Multiplier = 3.0
	// HistoryDays of hourly usage make up a key's normal range.
	HistoryDays = 14
	// Org is the id of the org-wide "scary day" alert. `keywatch ack org 6h`
	// silences it.
	Org = "org"
)

// Reasons an alert fired.
const (
	Spike    = "spike"     // one key's last hour is far above its normal
	ScaryDay = "scary_day" // org spend today is over the user's scary number
)

// Alert is one thing to tell the user about.
type Alert struct {
	KeyID  string // key id from the usage points, or Org for ScaryDay
	Reason string // Spike or ScaryDay
	// HourStart is the start of the last complete hour that was checked.
	HourStart     time.Time
	LastHourUSD   float64 // the key's spend in the last complete hour
	NormalHourUSD float64 // the key's 14-day p95 hourly spend (0 for a new key)
	LimitUSD      float64 // the threshold that was crossed
	TodayUSD      float64 // Spike: the key's spend today. ScaryDay: the org's.
	// TopKeyID and TopKeyUSD name the biggest spender today (ScaryDay only).
	TopKeyID  string
	TopKeyUSD float64
	Approx    bool // some spend was computed from tokens × price
}

type keyUsage struct {
	hist   [HistoryDays * 24]float64
	last   float64
	today  float64
	approx bool
}

// Check applies the alert rule from SPEC.md: a key's last complete hour is
// over max(3 × its 14-day p95 hourly spend, $5), or org spend today (UTC) is
// over scaryDayUSD. A scaryDayUSD of 0 turns the org-wide check off.
//
// acked maps key ids (or Org) to the time an ack expires. Ids match without
// regard to case, because GitHub stores variable names in upper case.
func Check(hourly []core.UsagePoint, now time.Time, scaryDayUSD float64, acked map[string]time.Time) []Alert {
	now = now.UTC()
	last := now.Truncate(time.Hour).Add(-time.Hour)
	histStart := last.Add(-HistoryDays * 24 * time.Hour)
	today := now.Truncate(24 * time.Hour)

	ackUntil := map[string]time.Time{}
	for id, t := range acked {
		ackUntil[strings.ToLower(id)] = t
	}
	isAcked := func(id string) bool {
		t, ok := ackUntil[strings.ToLower(id)]
		return ok && t.After(now)
	}

	keys := map[string]*keyUsage{}
	var ids []string
	orgApprox := false
	for _, p := range hourly {
		start := p.Start.UTC().Truncate(time.Hour)
		if start.After(now) {
			continue
		}
		u, ok := keys[p.KeyID]
		if !ok {
			u = &keyUsage{}
			keys[p.KeyID] = u
			ids = append(ids, p.KeyID)
		}
		u.approx = u.approx || p.Approx
		switch {
		case start.Equal(last):
			u.last += p.USD
		case !start.Before(histStart) && start.Before(last):
			u.hist[int(start.Sub(histStart)/time.Hour)] += p.USD
		}
		if !start.Before(today) {
			u.today += p.USD
			orgApprox = orgApprox || p.Approx
		}
	}
	sort.Strings(ids)

	var out []Alert
	orgToday, topID, topUSD := 0.0, "", 0.0
	for _, id := range ids {
		u := keys[id]
		orgToday += u.today
		if u.today > topUSD {
			topID, topUSD = id, u.today
		}
		normal := p95(u.hist[:])
		limit := math.Max(Multiplier*normal, MinHourUSD)
		if u.last > limit && !isAcked(id) {
			out = append(out, Alert{
				KeyID: id, Reason: Spike, HourStart: last,
				LastHourUSD: u.last, NormalHourUSD: normal, LimitUSD: limit,
				TodayUSD: u.today, Approx: u.approx,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastHourUSD > out[j].LastHourUSD })

	if scaryDayUSD > 0 && orgToday > scaryDayUSD && !isAcked(Org) {
		out = append(out, Alert{
			KeyID: Org, Reason: ScaryDay, HourStart: last,
			LimitUSD: scaryDayUSD, TodayUSD: orgToday,
			TopKeyID: topID, TopKeyUSD: topUSD, Approx: orgApprox,
		})
	}
	return out
}

// p95 is the nearest-rank 95th percentile. Hours with no spend count as 0.
func p95(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	rank := int(math.Ceil(0.95 * float64(len(s))))
	if rank < 1 {
		return 0
	}
	return s[rank-1]
}
