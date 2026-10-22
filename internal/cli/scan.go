package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/findings"
	"github.com/Kartik-007/keywatch/internal/inventory"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

const maxFindings = 5

var platformNames = map[string]string{"railway": "Railway", "vercel": "Vercel", "github": "GitHub"}

func init() {
	Root.RunE = runScan
	Root.Args = cobra.NoArgs
}

func runScan(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	start := time.Now()
	if !JSON {
		fmt.Println()
	}

	r := inventory.Load(ctx, inventory.Options{
		UseSource: askPlatforms(),
		Spend:     true,
	})
	cfg, _ := config.Load(".")
	applyLabels(r.Keys)
	fs := findings.From(r, cfg, time.Now())

	if JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"keys": jsonKeys(r.Keys), "findings": fs, "notes": r.Notes, "read_from": r.ReadFrom,
		})
	}

	fmt.Printf("  %s %s\n\n", ui.Bold.Render(fmt.Sprintf("%d AI %s found", countReal(r.Keys), plural(countReal(r.Keys), "key", "keys"))),
		ui.Dim.Render(fmt.Sprintf("(%.1fs)", time.Since(start).Seconds())))
	for _, k := range r.Keys {
		printKey(k)
	}

	// Not connected yet is itself the next step.
	var nudges []core.Finding
	for _, p := range provider.All() {
		if !contains(r.Connected, p.Name()) && hasProvider(r.Keys, p.Name()) {
			nudges = append(nudges, core.Finding{Title: fmt.Sprintf("Can't see %s spend yet", titleCase(p.Name())),
				Fix: "keywatch connect " + p.Name()})
		}
	}
	all := append(fs, nudges...)
	if len(all) > 0 {
		fmt.Printf("  %s\n", ui.Bold.Render(fmt.Sprintf("%d %s worth fixing", len(all), plural(len(all), "thing", "things"))))
		for i, f := range all {
			if i == maxFindings {
				fmt.Println(ui.Dim.Render(fmt.Sprintf("   …and %d more (keywatch --json for all)", len(all)-maxFindings)))
				break
			}
			mark := ui.Dim.Render(fmt.Sprintf("%2d.", i+1))
			switch f.Severity {
			case core.High:
				mark = ui.Red.Render(fmt.Sprintf("%2d.", i+1))
			case core.Medium:
				if f.Code != "" {
					mark = ui.Yellow.Render(fmt.Sprintf("%2d.", i+1))
				}
			}
			fmt.Printf("  %s %s\n      %s %s\n", mark, f.Title, ui.Dim.Render("→"), ui.Cmd.Render(f.Fix))
		}
	} else if countReal(r.Keys) > 0 {
		fmt.Printf("  %s\n", ui.Green.Render("Nothing risky found."))
	}

	for _, n := range r.Notes {
		fmt.Printf("\n  %s", ui.Dim.Render("Note: "+n))
	}
	for _, e := range r.Errors {
		fmt.Printf("\n  %s", ui.Yellow.Render("Couldn't read: "+e.Error()))
	}
	if len(r.Notes)+len(r.Errors) > 0 {
		fmt.Println()
	}
	fmt.Println()
	var readFrom []string
	for _, n := range r.ReadFrom {
		if p, ok := platformNames[n]; ok {
			n = p
		} else {
			n = titleCase(n)
		}
		readFrom = append(readFrom, n)
	}
	fmt.Println(ui.Receipt(readFrom, nil))
	fmt.Println()
	return nil
}

func printKey(k core.Key) {
	head := fmt.Sprintf("  %-14s %s", k.Redacted, ui.Bold.Render(k.Label))
	if k.Spend != nil {
		approx := ""
		if k.Spend.Approx {
			approx = "≈"
		}
		today := fmt.Sprintf("   %s$%.2f today", approx, k.Spend.Today)
		if k.Spend.Today > 5 && k.Spend.Today > 3*k.Spend.DailyP50 {
			today = ui.Red.Render(today + " ⚠")
		} else {
			today = ui.Dim.Render(today)
		}
		head += today + ui.Dim.Render(fmt.Sprintf(" · normal %s$%.2f/day", approx, k.Spend.DailyP50))
	}
	fmt.Println(head)
	if len(k.Locations) == 0 {
		fmt.Printf("  %-14s %s\n", "", ui.Yellow.Render("not found anywhere (still active at "+titleCase(k.Provider)+")"))
	}
	for _, l := range k.Locations {
		where := l.Where
		if l.Source != "local" && l.Source != "github" {
			where = platformNames[l.Source] + " · " + where
		}
		tag := ""
		switch {
		case l.Committed:
			tag = ui.Red.Render("  ⚠ committed to git")
		case l.NameOnly:
			tag = ui.Dim.Render("  (matched by name)")
		}
		fmt.Printf("  %-14s %s%s\n", "", ui.Dim.Render("· ")+where, tag)
	}
	fmt.Println()
}


func jsonKeys(keys []core.Key) []map[string]any {
	var out []map[string]any
	for _, k := range keys {
		m := map[string]any{"id": k.ID, "label": k.Label, "provider": k.Provider, "redacted": k.Redacted, "locations": k.Locations}
		if k.Spend != nil {
			m["spend"] = k.Spend
		}
		if k.Remote != nil {
			m["provider_key_id"] = k.Remote.ID
		}
		out = append(out, m)
	}
	return out
}

func countReal(keys []core.Key) int {
	n := 0
	for _, k := range keys {
		if k.ID != "?" {
			n++
		}
	}
	return n
}

func hasProvider(keys []core.Key, p string) bool {
	for _, k := range keys {
		if k.Provider == p {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func titleCase(s string) string {
	switch s {
	case core.OpenAI:
		return "OpenAI"
	case core.Anthropic:
		return "Anthropic"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}
