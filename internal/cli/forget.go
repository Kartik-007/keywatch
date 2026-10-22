package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/creds"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

var forgetCmd = &cobra.Command{
	Use:       "forget [provider]",
	Short:     "Remove stored admin keys from your keychain (all providers if none given)",
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: []string{core.OpenAI, core.Anthropic},
	RunE:      runForget,
}

func init() { Root.AddCommand(forgetCmd) }

func runForget(cmd *cobra.Command, args []string) error {
	names := []string{core.OpenAI, core.Anthropic}
	if len(args) == 1 {
		if _, err := lookupProvider(args[0]); err != nil {
			return err
		}
		names = args
	}
	var disp []string
	for _, n := range names {
		disp = append(disp, displayName[n])
	}

	noun, q := "admin key", "Remove it?"
	if len(names) > 1 {
		noun, q = "admin keys", "Remove them?"
	}
	fmt.Println()
	fmt.Printf("  This removes the stored %s %s from your %s.\n", strings.Join(disp, " and "), noun, keychainName())
	fmt.Println(ui.Dim.Render("  Your keys at the provider are not touched. Reconnect any time with keywatch connect."))
	if !ui.Confirm(q, false) {
		fmt.Println(ui.Receipt(nil, nil))
		return nil
	}

	var changed []string
	for _, n := range names {
		if err := creds.Forget(n); err != nil {
			return fmt.Errorf("removing the %s key: %w", displayName[n], err)
		}
		changed = append(changed, "removed the "+displayName[n]+" admin key from your keychain")
		if os.Getenv(creds.EnvVar(n)) != "" {
			fmt.Println(ui.Yellow.Render("  " + creds.EnvVar(n) + " is still set in your environment; unset it too."))
		}
	}
	fmt.Println("  " + ui.Green.Render("✓ Forgotten."))
	fmt.Println(ui.Receipt(nil, changed))
	return nil
}
