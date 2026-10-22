package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Kartik-007/keywatch/internal/inventory"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/source"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Exit non-zero if an AI key is committed to git (for CI and pre-commit)",
	Long: `Scans this folder and its git history for committed AI keys. Offline and
fast: no platforms, no providers, no network.`,
	Args: cobra.NoArgs,
	RunE: runCheck,
}

func init() { Root.AddCommand(checkCmd) }

type committedKey struct {
	ID       string   `json:"id"`
	Provider string   `json:"provider"`
	Redacted string   `json:"redacted"`
	Files    []string `json:"files"`
}

func runCheck(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	// Offline: hide the providers for this run so inventory never calls them.
	saved := provider.All()
	provider.Reset()
	defer provider.Reset(saved...)
	res := inventory.Load(cmd.Context(), inventory.Options{
		UseSource: func(source.Source) bool { return false },
	})

	var bad []committedKey
	for _, k := range res.Keys {
		var files []string
		for _, l := range k.Locations {
			if l.Committed {
				files = append(files, l.Where)
			}
		}
		if len(files) > 0 {
			bad = append(bad, committedKey{k.ID, k.Provider, k.Redacted, files})
		}
	}

	if JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if bad == nil {
			bad = []committedKey{}
		}
		if err := enc.Encode(map[string]any{"committed": bad}); err != nil {
			return err
		}
	} else {
		for _, k := range bad {
			for _, f := range k.Files {
				fmt.Fprintf(out, "  %s %s (%s) is committed to git in %s\n", ui.Red.Render("✗"), k.Redacted, k.Provider, f)
			}
		}
		if len(bad) > 0 {
			fmt.Fprintln(out, "  Treat these keys as leaked: run "+ui.Cmd.Render("keywatch")+" to see where else they live and what to do.")
		}
	}

	var errs []error
	for _, err := range res.Errors {
		errs = append(errs, fmt.Errorf("couldn't finish the scan: %w", err))
	}
	if len(bad) > 0 {
		errs = append(errs, fmt.Errorf("%d AI key(s) committed to git", len(bad)))
	}
	if len(errs) == 0 && !JSON {
		fmt.Fprintln(out, "  "+ui.Green.Render("✓")+" No AI keys committed to git.")
	}
	return errors.Join(errs...)
}
