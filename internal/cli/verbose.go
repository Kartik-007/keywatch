package cli

import (
	"fmt"
	"os"

	"github.com/Kartik-007/keywatch/internal/alert"
	"github.com/Kartik-007/keywatch/internal/provider/anthropic"
	"github.com/Kartik-007/keywatch/internal/provider/openai"
	"github.com/Kartik-007/keywatch/internal/source/github"
	"github.com/Kartik-007/keywatch/internal/source/railway"
	"github.com/Kartik-007/keywatch/internal/source/vercel"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/spf13/cobra"
)

// Every package that talks to the network has its own Logf. --verbose points
// all of them at stderr, so users can see each call (method, host, path).
func init() {
	cobra.OnInitialize(func() {
		ua := "keywatch/" + Root.Version
		openai.UserAgent, anthropic.UserAgent = ua, ua
		if !Verbose {
			return
		}
		logf := func(format string, a ...any) {
			fmt.Fprintln(os.Stderr, ui.Dim.Render("  → "+fmt.Sprintf(format, a...)))
		}
		openai.Logf, anthropic.Logf = logf, logf
		railway.Logf, vercel.Logf, github.Logf = logf, logf, logf
		alert.Logf = logf
	})
}
