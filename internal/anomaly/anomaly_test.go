package anomaly

import (
	"testing"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
)

var (
	now  = time.Date(2026, 10, 21, 14, 20, 0, 0, time.UTC)
	last = time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC) // last complete hour
)

// steady spends usd every hour of the 14-day history window.
func steady(id string, usd float64) []core.UsagePoint {
	var pts []core.UsagePoint
	for h := 1; h <= HistoryDays*24; h++ {
		pts = append(pts, core.UsagePoint{KeyID: id, Start: last.Add(-time.Duration(h) * time.Hour), USD: usd})
	}
	return pts
}

func at(id string, start time.Time, usd float64) core.UsagePoint {
	return core.UsagePoint{KeyID: id, Start: start, USD: usd}
}

func join(groups ...[]core.UsagePoint) []core.UsagePoint {
	var out []core.UsagePoint
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func TestCheck(t *testing.T) {
	// 20 of 336 history hours at $10 puts p95 at $10.
	var bursty []core.UsagePoint
	for h := 1; h <= 20; h++ {
		bursty = append(bursty, at("b", last.Add(-time.Duration(h*7)*time.Hour), 10))
	}

	tests := []struct {
		name  string
		pts   []core.UsagePoint
		scary float64
		acked map[string]time.Time
		want  []Alert // compared on KeyID, Reason, and the money fields
	}{
		{name: "no usage at all"},
		{
			name: "brand-new key under the $5 floor",
			pts:  []core.UsagePoint{at("a91f", last, 3)},
		},
		{
			name: "brand-new key over the $5 floor",
			pts:  []core.UsagePoint{at("a91f", last, 6)},
			want: []Alert{{KeyID: "a91f", Reason: Spike, LastHourUSD: 6, NormalHourUSD: 0, LimitUSD: 5, TodayUSD: 6}},
		},
		{
			name: "steady key, normal hour",
			pts:  join(steady("a91f", 1), []core.UsagePoint{at("a91f", last, 2)}),
		},
		{
			name: "steady key, spike over the floor",
			pts:  join(steady("a91f", 1), []core.UsagePoint{at("a91f", last, 10)}),
			want: []Alert{{KeyID: "a91f", Reason: Spike, LastHourUSD: 10, NormalHourUSD: 1, LimitUSD: 5, TodayUSD: 10 + 13}},
		},
		{
			name: "busy key under 3x normal",
			pts:  join(steady("a91f", 4), []core.UsagePoint{at("a91f", last, 11)}),
		},
		{
			name: "busy key over 3x normal",
			pts:  join(steady("a91f", 4), []core.UsagePoint{at("a91f", last, 13)}),
			want: []Alert{{KeyID: "a91f", Reason: Spike, LastHourUSD: 13, NormalHourUSD: 4, LimitUSD: 12, TodayUSD: 13 + 13*4}},
		},
		{
			name: "occasional bursts raise the normal range",
			pts:  join(bursty, []core.UsagePoint{at("b", last, 25)}),
		},
		{
			name:  "ack in force",
			pts:   []core.UsagePoint{at("a91f", last, 50)},
			acked: map[string]time.Time{"a91f": now.Add(time.Hour)},
		},
		{
			name:  "ack matches regardless of case",
			pts:   []core.UsagePoint{at("igAA", last, 50)},
			acked: map[string]time.Time{"IGAA": now.Add(time.Hour)},
		},
		{
			name:  "ack expired",
			pts:   []core.UsagePoint{at("a91f", last, 50)},
			acked: map[string]time.Time{"a91f": now.Add(-time.Minute)},
			want:  []Alert{{KeyID: "a91f", Reason: Spike, LastHourUSD: 50, LimitUSD: 5, TodayUSD: 50}},
		},
		{
			name:  "ack on another key doesn't silence this one",
			pts:   []core.UsagePoint{at("a91f", last, 50)},
			acked: map[string]time.Time{"3e0b": now.Add(time.Hour)},
			want:  []Alert{{KeyID: "a91f", Reason: Spike, LastHourUSD: 50, LimitUSD: 5, TodayUSD: 50}},
		},
		{
			name: "current partial hour is not the last hour",
			pts:  []core.UsagePoint{at("a91f", last.Add(time.Hour), 100)},
		},
		{
			name:  "yesterday's spend doesn't count as last hour or today",
			pts:   []core.UsagePoint{at("a91f", last.Add(-20*time.Hour), 100)},
			scary: 50,
		},
		{
			name:  "org-wide scary day",
			pts:   []core.UsagePoint{at("a91f", last.Add(-3*time.Hour), 4), at("a91f", last.Add(-2*time.Hour), 26), at("3e0b", last, 4), at("3e0b", last.Add(time.Hour), 21)},
			scary: 50,
			want:  []Alert{{KeyID: Org, Reason: ScaryDay, LimitUSD: 50, TodayUSD: 55, TopKeyID: "a91f", TopKeyUSD: 30}},
		},
		{
			name:  "org-wide under the scary number",
			pts:   []core.UsagePoint{at("a91f", last.Add(-2*time.Hour), 30), at("3e0b", last, 4)},
			scary: 50,
		},
		{
			name:  "scary day off",
			pts:   []core.UsagePoint{at("a91f", last.Add(-2*time.Hour), 300)},
			scary: 0,
		},
		{
			name:  "scary day acked, key spike still fires",
			pts:   []core.UsagePoint{at("a91f", last, 60)},
			scary: 50,
			acked: map[string]time.Time{"org": now.Add(time.Hour)},
			want:  []Alert{{KeyID: "a91f", Reason: Spike, LastHourUSD: 60, LimitUSD: 5, TodayUSD: 60}},
		},
		{
			name:  "acked key still counts toward the scary day",
			pts:   []core.UsagePoint{at("a91f", last, 60)},
			scary: 50,
			acked: map[string]time.Time{"a91f": now.Add(time.Hour)},
			want:  []Alert{{KeyID: Org, Reason: ScaryDay, LimitUSD: 50, TodayUSD: 60, TopKeyID: "a91f", TopKeyUSD: 60}},
		},
		{
			name: "several spikes, biggest first",
			pts:  []core.UsagePoint{at("a91f", last, 8), at("3e0b", last, 40)},
			want: []Alert{
				{KeyID: "3e0b", Reason: Spike, LastHourUSD: 40, LimitUSD: 5, TodayUSD: 40},
				{KeyID: "a91f", Reason: Spike, LastHourUSD: 8, LimitUSD: 5, TodayUSD: 8},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Check(tt.pts, now, tt.scary, tt.acked)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d alerts, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				g := got[i]
				if g.KeyID != w.KeyID || g.Reason != w.Reason || !near(g.LastHourUSD, w.LastHourUSD) ||
					!near(g.NormalHourUSD, w.NormalHourUSD) || !near(g.LimitUSD, w.LimitUSD) ||
					!near(g.TodayUSD, w.TodayUSD) || g.TopKeyID != w.TopKeyID || !near(g.TopKeyUSD, w.TopKeyUSD) {
					t.Errorf("alert %d:\n got %+v\nwant %+v", i, g, w)
				}
				if !g.HourStart.Equal(last) {
					t.Errorf("HourStart = %v, want %v", g.HourStart, last)
				}
			}
		})
	}
}

func TestCheckApprox(t *testing.T) {
	pts := []core.UsagePoint{{KeyID: "77c2", Start: last, USD: 9, Approx: true}}
	got := Check(pts, now, 1, nil)
	if len(got) != 2 || !got[0].Approx || !got[1].Approx {
		t.Fatalf("want both alerts marked approx, got %+v", got)
	}
}

func near(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }
