package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

var labelCmd = &cobra.Command{
	Use:   "label <id> <name>",
	Short: "Rename a key (writes .keywatch.toml so your team sees the same name)",
	Args:  cobra.ExactArgs(2),
	RunE:  runLabel,
}

func init() { Root.AddCommand(labelCmd) }

func runLabel(cmd *cobra.Command, args []string) error {
	id, name := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
	if id == "" || name == "" {
		return errors.New("usage: keywatch label <id> <name>, e.g. keywatch label a91f support-bot")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", config.File, err)
	}

	fmt.Println()
	if old := cfg.Labels[id]; old != "" {
		fmt.Printf("  Will change the label for key %s from %q to %q in ./%s.\n", id, old, name, config.File)
	} else {
		fmt.Printf("  Will label key %s as %q in ./%s.\n", id, name, config.File)
	}
	fmt.Println(ui.Dim.Render("  Commit the file to share the name with your team."))
	if !ui.Confirm("Write it?", false) {
		fmt.Println(ui.Receipt(nil, nil))
		return nil
	}
	cfg.Labels[id] = name
	if err := cfg.Save(dir); err != nil {
		return fmt.Errorf("writing %s: %w", config.File, err)
	}
	fmt.Println("  " + ui.Green.Render("✓ Labeled."))
	fmt.Println(ui.Receipt(nil, []string{"wrote ./" + config.File}))
	return nil
}
