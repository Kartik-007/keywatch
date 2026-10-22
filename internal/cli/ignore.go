package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

var ignoreReason string

var ignoreCmd = &cobra.Command{
	Use:   "ignore <finding> <key>",
	Short: "Stop showing a finding for a key, recorded in .keywatch.toml so the team shares it",
	Example: `  keywatch ignore F2 a91f --reason "shared on purpose, both services are internal"
  keywatch ignore F3 9c41 --reason "CI key, lives in an org secret"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, key := strings.ToUpper(args[0]), strings.ToLower(args[1])
		if !regexp.MustCompile(`^F[1-5]$`).MatchString(code) {
			return fmt.Errorf("finding must be F1–F5 (see keywatch --json for codes), got %q", args[0])
		}
		if strings.TrimSpace(ignoreReason) == "" {
			return fmt.Errorf(`add a reason so teammates know why: --reason "…"`)
		}
		cfg, err := config.Load(".")
		if err != nil {
			return err
		}
		if cfg.Ignored(code, key) {
			fmt.Println("  Already ignored.")
			return nil
		}
		fmt.Printf("\n  Will add to %s:\n    ignore %s for %s: %s\n\n", config.File, code, key, ignoreReason)
		if !ui.Confirm("Write it?", false) {
			return nil
		}
		cfg.Ignores = append(cfg.Ignores, config.Ignore{Finding: code, KeyID: key, Reason: ignoreReason})
		if err := cfg.Save("."); err != nil {
			return err
		}
		fmt.Println(ui.Receipt(nil, []string{config.File}))
		fmt.Println(ui.Dim.Render("  Commit " + config.File + " so your team shares this decision."))
		return nil
	},
}

func init() {
	ignoreCmd.Flags().StringVar(&ignoreReason, "reason", "", "why this is fine (required)")
	Root.AddCommand(ignoreCmd)
}
