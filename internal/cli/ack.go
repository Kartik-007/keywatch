package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/anomaly"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/Kartik-007/keywatch/internal/watch"
	"github.com/spf13/cobra"
)

var ackCmd = &cobra.Command{
	Use:   "ack <id> <duration>",
	Short: "Silence watch alerts for a key for a while, e.g. keywatch ack a91f 6h",
	Long: `Sets the repo variable KEYWATCH_ACK_<id> through gh. The hourly check stays
quiet about that key until the time runs out. Use "org" as the id to silence the
daily-total alert. Durations: 30m, 6h, 3d, 1w.`,
	Args: cobra.ExactArgs(2),
	RunE: runAck,
}

func init() { Root.AddCommand(ackCmd) }

// ackNow is swapped in tests.
var ackNow = time.Now

var ackID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

const maxAck = 90 * 24 * time.Hour

func runAck(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	id := args[0]
	if !ackID.MatchString(id) {
		return fmt.Errorf("%q isn't a key id; use the id from keywatch status or the alert, e.g. a91f", id)
	}
	d, err := parseAckDuration(args[1])
	if err != nil {
		return err
	}
	until := ackNow().Add(d).UTC().Truncate(time.Second)
	name := watch.AckVar(id)
	what := "key " + id
	if strings.EqualFold(id, anomaly.Org) {
		what = "the daily-total alert"
	}

	fmt.Fprintln(out, ui.Bold.Render("  Plan"))
	fmt.Fprintf(out, "    variable  %s = %s\n", name, until.Format(time.RFC3339))
	fmt.Fprintf(out, "  The hourly check stays quiet about %s until %s.\n\n", what, until.Local().Format("Mon 2 Jan 15:04 MST"))
	if !confirmWrite("Set it?", false) {
		fmt.Fprintln(out, ui.Receipt(nil, nil))
		return nil
	}
	if err := ghc.VariableSet(name, until.Format(time.RFC3339)); err != nil {
		return err
	}
	fmt.Fprintf(out, "  %s Undo early with: %s\n", ui.Green.Render("Acked."), ui.Cmd.Render("gh variable delete "+name))
	fmt.Fprintln(out, ui.Receipt([]string{"github"}, []string{"variable " + name}))
	return nil
}

// parseAckDuration accepts Go durations (30m, 6h, 1h30m) plus whole days and
// weeks (3d, 1w), up to 90 days.
func parseAckDuration(s string) (time.Duration, error) {
	bad := fmt.Errorf("%q isn't a duration; try 6h, 3d or 1w", s)
	var d time.Duration
	if n := len(s); n > 1 && (s[n-1] == 'd' || s[n-1] == 'w') {
		v, err := strconv.Atoi(s[:n-1])
		if err != nil {
			return 0, bad
		}
		unit := 24 * time.Hour
		if s[n-1] == 'w' {
			unit *= 7
		}
		d = time.Duration(v) * unit
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, bad
		}
	}
	if d <= 0 {
		return 0, bad
	}
	if d > maxAck {
		return 0, errors.New("acks last at most 90 days; to stop watching a key for good, disable it")
	}
	return d, nil
}
