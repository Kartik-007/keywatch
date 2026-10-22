// Package cli wires commands. Each command lives in its own file and adds
// itself to Root in init(), so files can be added without editing this one.
package cli

import (
	"github.com/Kartik-007/keywatch/internal/demo"
	"github.com/spf13/cobra"

	// Register sources and providers.
	_ "github.com/Kartik-007/keywatch/internal/provider/anthropic"
	_ "github.com/Kartik-007/keywatch/internal/provider/openai"
	_ "github.com/Kartik-007/keywatch/internal/source/github"
	_ "github.com/Kartik-007/keywatch/internal/source/local"
	_ "github.com/Kartik-007/keywatch/internal/source/railway"
	_ "github.com/Kartik-007/keywatch/internal/source/vercel"
)

var Version = "dev"

// Global flags, readable by every command.
var (
	JSON    bool
	Verbose bool
	Demo    bool
	Yes     bool // assume yes to read-only prompts (never to writes)
)

var Root = &cobra.Command{
	Use:           "keywatch",
	Short:         "Find every AI key you've deployed, see what it spends, get pinged when it spikes.",
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if Demo {
			demo.Enable()
		}
	},
}

func init() {
	f := Root.PersistentFlags()
	f.BoolVar(&JSON, "json", false, "machine-readable output")
	f.BoolVarP(&Verbose, "verbose", "v", false, "log every network call")
	f.BoolVar(&Demo, "demo", false, "run against built-in demo data (no network, fake keys)")
	f.BoolVarP(&Yes, "yes", "y", false, "answer yes to read-only prompts")
}
