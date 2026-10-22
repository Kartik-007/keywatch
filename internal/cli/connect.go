package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/creds"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/provider/anthropic"
	"github.com/Kartik-007/keywatch/internal/provider/openai"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:       "connect openai|anthropic",
	Short:     "Store a provider admin key so keywatch can read each key's spend",
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{core.OpenAI, core.Anthropic},
	RunE:      runConnect,
}

func init() {
	Root.AddCommand(connectCmd)
	// Provider HTTP clients: identify as keywatch/<version> and, with
	// --verbose, log every call (method + host + path only).
	cobra.OnInitialize(func() {
		ua := "keywatch/" + Root.Version
		openai.UserAgent, anthropic.UserAgent = ua, ua
		if Verbose {
			logf := func(format string, a ...any) {
				fmt.Fprintln(os.Stderr, ui.Dim.Render("  → "+fmt.Sprintf(format, a...)))
			}
			openai.Logf, anthropic.Logf = logf, logf
		}
	})
}

var apiHost = map[string]string{
	core.OpenAI:    "api.openai.com",
	core.Anthropic: "api.anthropic.com",
}

var displayName = map[string]string{
	core.OpenAI:    "OpenAI",
	core.Anthropic: "Anthropic",
}

func lookupProvider(name string) (provider.Provider, error) {
	p, ok := provider.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (use openai or anthropic)", name)
	}
	return p, nil
}

func keychainName() string {
	if runtime.GOOS == "darwin" {
		return "macOS Keychain"
	}
	return "system keyring (Secret Service)"
}

func runConnect(cmd *cobra.Command, args []string) error {
	name := args[0]
	p, err := lookupProvider(name)
	if err != nil {
		return err
	}
	if Demo {
		return errors.New("connect is off in --demo; demo data needs no keys")
	}
	disp := displayName[name]

	fmt.Println()
	fmt.Println("  " + ui.Bold.Render("Connect "+disp) + ui.Dim.Render(" · unlocks spend per key, key age, and alerts"))
	fmt.Println()
	fmt.Println("  How your admin key is handled:")
	fmt.Println("    • Stored in your " + keychainName() + ", never in a file.")
	fmt.Println("    • Only ever sent to " + apiHost[name] + ".")
	fmt.Println("    • Remove it any time: " + ui.Cmd.Render("keywatch forget "+name))
	fmt.Println()
	switch name {
	case core.OpenAI:
		fmt.Println("  Create an admin key (organization owners only) and pick " + ui.Bold.Render("Read only") + " permissions.")
		fmt.Println(ui.Dim.Render("  keywatch only reads. `keywatch disable` needs a key with write access; with a read-only key you'd delete keys in the dashboard instead."))
	case core.Anthropic:
		fmt.Println("  Create an admin key. The Admin API only works for " + ui.Bold.Render("organization") + " accounts, not individual ones.")
		fmt.Println(ui.Dim.Render("  Anthropic admin keys can't be made read-only, which is why keywatch keeps it in the keychain and asks before every change."))
	}
	fmt.Println("  Opening " + ui.Cmd.Render(p.AdminKeyURL()))
	openBrowser(p.AdminKeyURL())
	fmt.Println()

	key, err := ui.AskSecret("Paste the admin key (hidden):")
	if err != nil {
		return fmt.Errorf("couldn't read the key from the terminal: %w", err)
	}
	if key == "" {
		fmt.Println(ui.Receipt(nil, nil))
		return errors.New("no key entered")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()
	if err := p.Connect(ctx, key); err != nil {
		fmt.Println(ui.Receipt([]string{name}, nil))
		return err
	}
	if err := creds.Set(name, key); err != nil {
		return fmt.Errorf("the key works, but saving it to the %s failed: %w", keychainName(), err)
	}
	changed := []string{"saved the " + disp + " admin key to your " + keychainName()}

	fmt.Println("  " + ui.Green.Render("✓ Connected.") + " " + weekSummary(ctx, p, key))
	fmt.Println()
	fmt.Println("  Next: " + ui.Cmd.Render("keywatch status") + ui.Dim.Render(" shows spend per key and where each one lives."))
	fmt.Println()
	fmt.Println(ui.Receipt([]string{name}, changed))
	return nil
}

// weekSummary is the "real data right away" line: last 7 days of spend.
func weekSummary(ctx context.Context, p provider.Provider, key string) string {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	pts, err := p.Usage(ctx, key, today.AddDate(0, 0, -6), today.AddDate(0, 0, 1), "1d")
	if err != nil {
		return ui.Yellow.Render("Couldn't read spend yet: " + err.Error())
	}
	var total float64
	approx := false
	spent := map[string]bool{}
	for _, pt := range pts {
		total += pt.USD
		approx = approx || pt.Approx
		if pt.USD > 0 {
			spent[pt.KeyID] = true
		}
	}
	noun := "keys"
	if len(spent) == 1 {
		noun = "key"
	}
	line := fmt.Sprintf("Last 7 days: %s across %d %s.", money(total, approx), len(spent), noun)
	if keys, err := p.ListKeys(ctx, key); err == nil {
		active := 0
		for _, k := range keys {
			if k.Active {
				active++
			}
		}
		line += ui.Dim.Render(fmt.Sprintf(" (%d active keys in total)", active))
	}
	return line
}

func openBrowser(url string) {
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux", "freebsd", "openbsd", "netbsd":
		name = "xdg-open"
	default:
		return
	}
	if _, err := exec.LookPath(name); err != nil {
		return
	}
	_ = exec.Command(name, url).Start()
}
