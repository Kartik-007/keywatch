package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/Kartik-007/keywatch/internal/alert"
	"github.com/Kartik-007/keywatch/internal/anomaly"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/Kartik-007/keywatch/internal/watch"
	"github.com/spf13/cobra"
)

var alertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "Also send watch alerts to Slack or Discord",
	Long: `By default a watch alert fails the GitHub Action and GitHub emails you.
These commands add a Slack or Discord incoming webhook as well.`,
}

func init() {
	for _, k := range []alert.Kind{alert.Slack, alert.Discord} {
		alertsCmd.AddCommand(&cobra.Command{
			Use:   string(k) + " <webhook-url>",
			Short: "Send watch alerts to a " + kindName(k) + " incoming webhook",
			Args:  cobra.ExactArgs(1),
			RunE:  setWebhook(k),
		})
	}
	alertsCmd.AddCommand(&cobra.Command{
		Use:   "test [webhook-url]",
		Short: "Send a sample alert (to KEYWATCH_WEBHOOK or the URL given)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runAlertsTest,
	})
	Root.AddCommand(alertsCmd)
}

func kindName(k alert.Kind) string {
	if k == alert.Slack {
		return "Slack"
	}
	return "Discord"
}

func setWebhook(kind alert.Kind) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		url := args[0]
		got, err := alert.KindOf(url)
		if err != nil {
			return err
		}
		if got != kind {
			return fmt.Errorf("that's a %s webhook; use keywatch alerts %s", kindName(got), got)
		}
		if Verbose {
			alert.Logf = func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) }
		}

		fmt.Fprintln(out, ui.Bold.Render("  Plan"))
		fmt.Fprintf(out, "    secret  %s  your %s webhook, encrypted by gh\n", watch.WebhookEnv, kindName(kind))
		fmt.Fprintln(out)
		if !confirmWrite("Set it?", false) {
			fmt.Fprintln(out, ui.Receipt(nil, nil))
			return nil
		}
		if err := ghc.SecretSet(watch.WebhookEnv, url); err != nil {
			return err
		}
		changed := []string{"secret " + watch.WebhookEnv}
		fmt.Fprintf(out, "  %s Watch alerts will also go to %s.\n", ui.Green.Render("Done."), kindName(kind))
		workflowNote(out)

		if confirmWrite("Send a test alert there now?", true) {
			if err := alert.Post(cmd.Context(), url, sampleAlert()); err != nil {
				fmt.Fprintln(out, ui.Receipt([]string{"github"}, changed))
				return err
			}
			fmt.Fprintf(out, "  Sent. Check %s.\n", kindName(kind))
		}
		fmt.Fprintln(out, ui.Receipt([]string{"github"}, changed))
		return nil
	}
}

func runAlertsTest(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	text := sampleAlert()
	fmt.Fprintln(out, text)
	url := os.Getenv(watch.WebhookEnv)
	if len(args) == 1 {
		url = args[0]
	}
	if url == "" {
		fmt.Fprintln(out, "  That's what an alert looks like. GitHub keeps the webhook secret, so keywatch")
		fmt.Fprintln(out, "  can't read it back. To send this sample, pass the URL:")
		fmt.Fprintln(out, ui.Cmd.Render("    keywatch alerts test <webhook-url>"))
		return nil
	}
	kind, err := alert.KindOf(url)
	if err != nil {
		return err
	}
	if Verbose {
		alert.Logf = func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) }
	}
	if err := alert.Post(cmd.Context(), url, text); err != nil {
		return err
	}
	fmt.Fprintf(out, "  Sent a sample alert to %s.\n", kindName(kind))
	return nil
}

// sampleAlert is a made-up spike, clearly marked as a test.
func sampleAlert() string {
	a := anomaly.Alert{
		KeyID: "a91f", Reason: anomaly.Spike,
		HourStart:   time.Now().UTC().Truncate(time.Hour).Add(-time.Hour),
		LastHourUSD: 42.10, NormalHourUSD: 4.50, LimitUSD: 13.50, TodayUSD: 88,
	}
	k := core.Key{
		ID: "a91f", Label: "support-bot", Provider: core.OpenAI, Redacted: "sk-…a91f",
		Locations: []core.Location{{Source: "railway", Where: "support-bot · production", Var: "OPENAI_API_KEY"}},
	}
	return "TEST: this is a sample from `keywatch alerts test`. Nothing is wrong.\n\n" + alert.Render(a, k)
}
