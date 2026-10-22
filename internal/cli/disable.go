package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/creds"
	"github.com/Kartik-007/keywatch/internal/inventory"
	"github.com/Kartik-007/keywatch/internal/provider/anthropic"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

var disableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Turn a key off at the provider (Anthropic: reversible; OpenAI: deletes it)",
	Args:  cobra.ExactArgs(1),
	RunE:  runDisable,
}

var enableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Turn a disabled Anthropic key back on",
	Args:  cobra.ExactArgs(1),
	RunE:  runEnable,
}

func init() {
	Root.AddCommand(disableCmd)
	Root.AddCommand(enableCmd)
}

// findKey matches a user-typed id against the short id (last 4) or the
// provider's own key id.
func findKey(keys []core.Key, id string) (core.Key, error) {
	var hits []core.Key
	for _, k := range keys {
		if k.ID == id || (k.Remote != nil && k.Remote.ID == id) {
			hits = append(hits, k)
		}
	}
	switch len(hits) {
	case 0:
		return core.Key{}, fmt.Errorf("no key with id %q. Run keywatch status to see ids", id)
	case 1:
		return hits[0], nil
	}
	var names []string
	for _, k := range hits {
		n := k.Provider + " " + k.Redacted
		if k.Remote != nil {
			n += " (" + k.Remote.ID + ")"
		}
		names = append(names, n)
	}
	return core.Key{}, fmt.Errorf("%d keys end in %q: %s. Use the provider key id instead", len(hits), id, strings.Join(names, ", "))
}

func runDisable(cmd *cobra.Command, args []string) error {
	if Demo {
		return errors.New("disable is off in --demo; it would change a real key")
	}
	id := args[0]
	r := inventory.Load(cmd.Context(), inventory.Options{UseSource: askPlatforms()})
	applyLabels(r.Keys)
	printNotes(r)
	k, err := findKey(r.Keys, id)
	if err != nil {
		return err
	}
	disp := displayName[k.Provider]
	if k.Remote == nil {
		if !contains(r.Connected, k.Provider) {
			return fmt.Errorf("keywatch can't disable %s: %s isn't connected. Run: keywatch connect %s", k.Redacted, disp, k.Provider)
		}
		return fmt.Errorf("%s doesn't list %s. It may already be deleted, or belong to a different organization than your admin key", disp, k.Redacted)
	}
	if !k.Remote.Active {
		fmt.Printf("  %s is already inactive at %s. Nothing to do.\n", k.Redacted, disp)
		fmt.Println(ui.Receipt(r.ReadFrom, nil))
		return nil
	}
	p, err := lookupProvider(k.Provider)
	if err != nil {
		return err
	}
	adminKey := creds.Get(k.Provider)

	fmt.Println()
	fmt.Println("  " + ui.Bold.Render("keywatch disable") + ui.Dim.Render(" · level: Change"))
	fmt.Println()
	fmt.Printf("  Disable %s (%s) at %s.\n", ui.Bold.Render(k.Redacted), k.Label, disp)
	fmt.Println()
	if len(k.Locations) == 0 {
		fmt.Println("  keywatch didn't find this key deployed anywhere it can see.")
	} else {
		fmt.Println("  These will stop working:")
		for _, l := range k.Locations {
			fmt.Println("    • " + where(l))
		}
	}
	fmt.Println(ui.Dim.Render("  Anything else using this key (places keywatch can't see) stops too."))
	fmt.Println()
	if p.Reversible() {
		fmt.Println("  You can undo this with " + ui.Cmd.Render("keywatch enable "+k.ID) + ".")
	} else {
		fmt.Println("  " + ui.Red.Render("This deletes the key at "+disp+". It can't be undone.") + " You'll need a new key for the places above.")
	}
	if !ui.Confirm("Disable this key?", false) {
		fmt.Println(ui.Receipt(r.ReadFrom, nil))
		return nil
	}
	if !p.Reversible() {
		typed := ui.Ask(fmt.Sprintf("Type the last 4 characters of the key (%s) to delete it:", k.ID))
		if typed != k.ID {
			fmt.Println("  That didn't match. Nothing was deleted.")
			fmt.Println(ui.Receipt(r.ReadFrom, nil))
			return nil
		}
	}

	if err := p.Disable(cmd.Context(), adminKey, *k.Remote); err != nil {
		fmt.Println(ui.Receipt(r.ReadFrom, nil))
		return err
	}
	verb := "disabled"
	if !p.Reversible() {
		verb = "deleted"
	}
	fmt.Println("  " + ui.Green.Render("✓ "+strings.ToUpper(verb[:1])+verb[1:]+" "+k.Redacted+"."))
	fmt.Println(ui.Receipt(r.ReadFrom, []string{verb + " " + disp + " key " + k.Redacted}))
	return nil
}

func runEnable(cmd *cobra.Command, args []string) error {
	if Demo {
		return errors.New("enable is off in --demo; it would change a real key")
	}
	id := args[0]
	adminKey := creds.Get(core.Anthropic)
	if adminKey == "" {
		return errors.New("enable needs Anthropic connected. Run: keywatch connect anthropic")
	}
	p, err := lookupProvider(core.Anthropic)
	if err != nil {
		return err
	}
	keys, err := p.ListKeys(cmd.Context(), adminKey)
	if err != nil {
		return err
	}
	var hits []core.ProviderKey
	for _, k := range keys {
		if k.ID == id || (k.Hint != "" && core.Last4(k.Hint) == id) {
			hits = append(hits, k)
		}
	}
	readFrom := []string{core.Anthropic}
	switch {
	case len(hits) == 0:
		return fmt.Errorf("no Anthropic key with id %q. enable only works for Anthropic; OpenAI keys are deleted, not deactivated", id)
	case len(hits) > 1:
		return fmt.Errorf("%d Anthropic keys end in %q. Use the key id (apikey_…) instead", len(hits), id)
	}
	k := hits[0]
	if k.Active {
		fmt.Printf("  %s (%s) is already active. Nothing to do.\n", k.Hint, k.Name)
		fmt.Println(ui.Receipt(readFrom, nil))
		return nil
	}

	fmt.Println()
	fmt.Println("  " + ui.Bold.Render("keywatch enable") + ui.Dim.Render(" · level: Change"))
	fmt.Println()
	fmt.Printf("  Turn %s (%s) back on at Anthropic.\n", ui.Bold.Render(k.Hint), k.Name)
	fmt.Println(ui.Dim.Render("  Everything using it starts working again, including anyone who has a stolen copy."))
	if !ui.Confirm("Enable this key?", false) {
		fmt.Println(ui.Receipt(readFrom, nil))
		return nil
	}
	if err := anthropic.Enable(cmd.Context(), adminKey, k); err != nil {
		fmt.Println(ui.Receipt(readFrom, nil))
		return err
	}
	fmt.Println("  " + ui.Green.Render("✓ Enabled "+k.Hint+"."))
	fmt.Println(ui.Receipt(readFrom, []string{"enabled Anthropic key " + k.Hint}))
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
