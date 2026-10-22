package demo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/inventory"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/source"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

func TestEnableRegistersFakes(t *testing.T) {
	Enable()
	if n := len(source.All()); n != 4 {
		t.Fatalf("want 4 sources, got %d", n)
	}
	if n := len(provider.All()); n != 2 {
		t.Fatalf("want 2 providers, got %d", n)
	}
}

func TestHintsNeverHoldAFullKey(t *testing.T) {
	Enable()
	full := []string{testutil.OpenAI1, testutil.OpenAI2, testutil.Anthropic1}
	for _, p := range provider.All() {
		keys, _ := p.ListKeys(context.Background(), adminKey)
		for _, k := range keys {
			if m := core.Detect(k.Hint); len(m) > 0 {
				t.Errorf("%s hint for %s looks like a full key", p.Name(), k.ID)
			}
			for _, f := range full {
				if strings.Contains(k.Hint, f) {
					t.Errorf("%s hint for %s contains a full fake key", p.Name(), k.ID)
				}
			}
		}
	}
}

func TestStory(t *testing.T) {
	fixed := time.Date(2026, 10, 21, 15, 20, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	defer func() { now = time.Now }()
	Enable()

	r := inventory.Load(context.Background(), inventory.Options{Spend: true, Now: fixed})
	byID := map[string]core.Key{}
	for _, k := range r.Keys {
		byID[k.ID] = k
	}
	if len(byID) != 4 {
		t.Fatalf("want 4 keys, got %d: %+v", len(byID), r.Keys)
	}
	a := byID["a91f"]
	if len(a.Locations) != 3 || a.Remote == nil || a.Spend == nil {
		t.Fatalf("a91f should live in 3 places and have spend: %+v", a)
	}
	if a.Spend.Today < 41 || a.Spend.DailyP50 < 2 || a.Spend.DailyP50 > 4 {
		t.Errorf("a91f spend off: %+v", *a.Spend)
	}
	if k := byID["9c41"]; len(k.Locations) != 0 || k.Label != "old-laptop" {
		t.Errorf("9c41 should be found nowhere: %+v", k)
	}
	if got := r.Uncapped[core.OpenAI]; len(got) != 1 || got[0] != "prod" {
		t.Errorf("uncapped = %v", got)
	}

	// The last complete hour spikes; every other hour looks normal.
	p, _ := provider.Get(core.OpenAI)
	pts, _ := p.Usage(context.Background(), adminKey, fixed.Add(-14*24*time.Hour), fixed, "1h")
	var last, maxBefore float64
	for _, pt := range pts {
		if pt.KeyID != "key_demo_a91f" {
			continue
		}
		if pt.Start.Equal(fixed.Truncate(time.Hour).Add(-time.Hour)) {
			last = pt.USD
		} else if pt.USD > maxBefore {
			maxBefore = pt.USD
		}
	}
	if last < 40 || maxBefore > 1 {
		t.Errorf("last hour $%.2f, max before $%.2f", last, maxBefore)
	}
}
