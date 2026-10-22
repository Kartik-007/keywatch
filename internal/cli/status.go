package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/inventory"
	"github.com/Kartik-007/keywatch/internal/source"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "One row per key: where it lives, how old it is, what it spends",
	Args:  cobra.NoArgs,
	RunE:  runStatus,
}

func init() { Root.AddCommand(statusCmd) }

type statusRow struct {
	ID         string          `json:"id"`
	Label      string          `json:"label"`
	Provider   string          `json:"provider"`
	Redacted   string          `json:"redacted"`
	Locations  []core.Location `json:"locations"`
	ProviderID string          `json:"provider_key_id,omitempty"`
	Project    string          `json:"project,omitempty"`
	Active     *bool           `json:"active,omitempty"`
	CreatedAt  *time.Time      `json:"created_at,omitempty"`
	LastUsed   *time.Time      `json:"last_used,omitempty"`
	Spend      *statusSpend    `json:"spend,omitempty"`
}

type statusSpend struct {
	Today    float64 `json:"today_usd"`
	Last7d   float64 `json:"last_7d_usd"`
	DailyP50 float64 `json:"normal_day_usd"`
	Approx   bool    `json:"approx"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	now := time.Now()
	r := inventory.Load(cmd.Context(), inventory.Options{UseSource: askPlatforms(), Spend: true, Now: now})
	applyLabels(r.Keys)

	if JSON {
		return printStatusJSON(r)
	}

	fmt.Println()
	fmt.Println("  " + ui.Bold.Render("keywatch status") + ui.Dim.Render(" · "+levelLine(r)))
	fmt.Println()
	if len(r.Keys) == 0 {
		fmt.Println("  No AI keys found here or in the providers you've connected.")
	} else {
		printKeyTable(r.Keys, now)
	}
	fmt.Println()
	printConnectHint(r)
	printNotes(r)
	fmt.Println(ui.Receipt(r.ReadFrom, nil))
	return nil
}

func printKeyTable(keys []core.Key, now time.Time) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ID\tLABEL\tPROVIDER\tWHERE\tAGE\tTODAY\tLAST 7D\tNORMAL DAY")
	approx := false
	for _, k := range keys {
		ageStr := "—"
		if k.Remote != nil {
			ageStr = age(k.Remote.CreatedAt, now)
		}
		today, week, normal := "—", "—", "—"
		if k.Spend != nil {
			today = money(k.Spend.Today, k.Spend.Approx)
			week = money(k.Spend.Last7d, k.Spend.Approx)
			normal = money(k.Spend.DailyP50, k.Spend.Approx)
			approx = approx || k.Spend.Approx
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			k.ID, truncate(k.Label, 36), k.Provider, len(k.Locations), ageStr, today, week, normal)
	}
	tw.Flush()
	if approx {
		fmt.Println()
		fmt.Println(ui.Dim.Render("  ≈ estimated from tokens × list prices (Anthropic doesn't report cost per key)."))
	}
}

// printConnectHint says what connecting unlocks, for providers that have
// keys here but no admin key yet.
func printConnectHint(r inventory.Result) {
	connected := map[string]bool{}
	for _, c := range r.Connected {
		connected[c] = true
	}
	var missing []string
	for _, p := range []string{core.OpenAI, core.Anthropic} {
		if connected[p] {
			continue
		}
		if len(r.Connected) == 0 || hasProvider(r.Keys, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return
	}
	fmt.Println("  Connect a provider to see each key's age, spend today and over 7 days, and its normal day:")
	for _, p := range missing {
		fmt.Println("    " + ui.Cmd.Render("keywatch connect "+p))
	}
	fmt.Println()
}


func printStatusJSON(r inventory.Result) error {
	out := struct {
		Keys      []statusRow `json:"keys"`
		Connected []string    `json:"connected"`
		ReadFrom  []string    `json:"read_from"`
		Notes     []string    `json:"notes"`
		Errors    []string    `json:"errors"`
	}{Keys: []statusRow{}, Connected: orEmpty(r.Connected), ReadFrom: orEmpty(r.ReadFrom), Notes: orEmpty(r.Notes), Errors: []string{}}
	for _, k := range r.Keys {
		row := statusRow{ID: k.ID, Label: k.Label, Provider: k.Provider, Redacted: k.Redacted, Locations: k.Locations}
		if row.Locations == nil {
			row.Locations = []core.Location{}
		}
		if rm := k.Remote; rm != nil {
			active := rm.Active
			row.ProviderID, row.Project, row.Active = rm.ID, rm.Project, &active
			if !rm.CreatedAt.IsZero() {
				t := rm.CreatedAt
				row.CreatedAt = &t
			}
			if !rm.LastUsed.IsZero() {
				t := rm.LastUsed
				row.LastUsed = &t
			}
		}
		if s := k.Spend; s != nil {
			row.Spend = &statusSpend{Today: s.Today, Last7d: s.Last7d, DailyP50: s.DailyP50, Approx: s.Approx}
		}
		out.Keys = append(out.Keys, row)
	}
	for _, e := range r.Errors {
		out.Errors = append(out.Errors, e.Error())
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- helpers shared by status, connect and disable ---

// askPlatforms asks once whether to read deploy platforms that have an
// existing CLI login. --yes answers yes; --json never prompts and only reads
// them with --yes.
func askPlatforms() func(source.Source) bool {
	var avail []string
	for _, s := range source.All() {
		if s.Name() != "local" && s.Available() {
			avail = append(avail, platformNames[s.Name()])
		}
	}
	if len(avail) == 0 {
		return nil
	}
	ok := Yes
	if !ok && !JSON {
		fmt.Printf("  You're logged into %s on this machine.\n", joinAnd(avail))
		ok = ui.Confirm("Read env vars from them too? (read-only)", true)
		fmt.Println()
	}
	return func(source.Source) bool { return ok }
}

// applyLabels overrides derived labels with the ones in .keywatch.toml.
func applyLabels(keys []core.Key) {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return
	}
	for i, k := range keys {
		if l := cfg.Labels[k.ID]; l != "" {
			keys[i].Label = l
		}
	}
}

// levelLine states which access level this run operates at (SPEC: every run
// says so).
func levelLine(r inventory.Result) string {
	level := "Look"
	if len(r.ReadFrom) > len(r.Connected) {
		level = "Read platforms"
	}
	if len(r.Connected) > 0 {
		level = "Read spend"
	}
	from := append([]string{"local files"}, r.ReadFrom...)
	return "level: " + level + " (" + strings.Join(from, ", ") + ")"
}

func printNotes(r inventory.Result) {
	for _, n := range r.Notes {
		fmt.Println(ui.Dim.Render("  · " + n))
	}
	for _, e := range r.Errors {
		fmt.Fprintln(os.Stderr, ui.Yellow.Render("  ! "+e.Error()))
	}
	if len(r.Notes)+len(r.Errors) > 0 {
		fmt.Println()
	}
}

func money(v float64, approx bool) string {
	s := fmt.Sprintf("$%.2f", v)
	if v > 0 && v < 0.005 {
		s = "<$0.01"
	}
	if approx {
		s = "≈" + s
	}
	return s
}

func age(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days < 1:
		return "<1d"
	case days < 60:
		return fmt.Sprintf("%dd", days)
	case days < 730:
		return fmt.Sprintf("%dmo", days/30)
	default:
		return fmt.Sprintf("%dy", days/365)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// where renders one location as "railway · support-bot · production (OPENAI_API_KEY)".
func where(l core.Location) string {
	s := l.Source + " · " + l.Where
	if l.Var != "" {
		s += " (" + l.Var + ")"
	}
	if l.NameOnly {
		s += ui.Dim.Render(" matched by name only")
	}
	return s
}
