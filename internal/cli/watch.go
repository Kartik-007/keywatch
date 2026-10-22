package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Kartik-007/keywatch/internal/alert"
	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/creds"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/ui"
	"github.com/Kartik-007/keywatch/internal/watch"
	"github.com/spf13/cobra"
)

// GHClient is the slice of the GitHub CLI that watch, alerts and ack use.
// Tests swap ghc so they never shell out.
type GHClient interface {
	// SecretSet sets an encrypted repo secret. The value goes to gh on
	// stdin, never on the command line.
	SecretSet(name, value string) error
	SecretNames() ([]string, error)
	VariableSet(name, value string) error
}

var ghc GHClient = ghExec{}

// Prompts, swapped in tests.
var (
	askLine      = ui.Ask
	confirmWrite = ui.Confirm
)

type ghExec struct{}

func (ghExec) run(stdin string, args ...string) (string, error) {
	if Verbose {
		fmt.Fprintf(os.Stderr, "  exec gh %s\n", strings.Join(args, " "))
	}
	cmd := exec.Command("gh", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("this needs the GitHub CLI (gh), logged in to this repo: https://cli.github.com")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gh %s %s: %s", args[0], args[1], msg)
	}
	return stdout.String(), nil
}

// SecretSet leaves out --body, so gh reads the value from stdin.
func (g ghExec) SecretSet(name, value string) error {
	_, err := g.run(value, "secret", "set", name)
	return err
}

func (g ghExec) SecretNames() ([]string, error) {
	out, err := g.run("", "secret", "list", "--json", "name", "--jq", ".[].name")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (g ghExec) VariableSet(name, value string) error {
	_, err := g.run("", "variable", "set", name, "--body", value)
	return err
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Check AI spend every hour from a GitHub Action and alert when it looks wrong",
	Long: `Sets up .github/workflows/keywatch.yml in this repo. Every hour it reads each
key's spend, compares the last hour with the key's normal range, and fails the
job (so GitHub emails you) when spend looks wrong. Alerts arrive within the hour.`,
	Args: cobra.NoArgs,
	RunE: runWatchSetup,
}

var watchRunScaryDay float64

var watchRunCmd = &cobra.Command{
	Use:    "run",
	Short:  "The hourly check the GitHub Action runs",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runWatchRun,
}

func init() {
	watchRunCmd.Flags().Float64Var(&watchRunScaryDay, "scary-day", 0, "alert when org spend today passes this many USD (0: off)")
	watchCmd.AddCommand(watchRunCmd)
	Root.AddCommand(watchCmd)
}

const defaultScaryDay = 50

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func runWatchSetup(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	if _, err := os.Stat(".git"); err != nil {
		return errors.New("run keywatch watch from the root of the GitHub repo that should run the hourly check")
	}
	cfg, err := config.Load(".")
	if err != nil {
		return fmt.Errorf("reading %s: %w", config.File, err)
	}

	type secret struct{ name, value, what string }
	var secrets []secret
	var providers []string
	for _, p := range provider.All() {
		if v := creds.Get(p.Name()); v != "" {
			providers = append(providers, p.Name())
			secrets = append(secrets, secret{creds.EnvVar(p.Name()), v, "your " + p.Name() + " admin key, from the keychain"})
		}
	}
	if len(providers) == 0 {
		return errors.New("connect a provider first, so the hourly check can read spend: keywatch connect openai (or anthropic)")
	}

	fmt.Fprintln(out, "  keywatch watch adds a GitHub Action to this repo that checks each key's spend")
	fmt.Fprintln(out, "  every hour. When spend looks wrong the job fails and GitHub emails you, so")
	fmt.Fprintln(out, "  alerts arrive within the hour. Nothing runs on this machine.")
	fmt.Fprintln(out)

	def := cfg.ScaryDay
	if def <= 0 {
		def = defaultScaryDay
	}
	scary := def
	ans := askLine(fmt.Sprintf("What's a scary amount for one day of AI spend? [$%s]", fmtUSD(def)))
	if ans = strings.NewReplacer("$", "", ",", "").Replace(strings.TrimSpace(ans)); ans != "" {
		scary, err = strconv.ParseFloat(ans, 64)
		if err != nil || scary <= 0 {
			return fmt.Errorf("%q isn't an amount in dollars, like 50", ans)
		}
	}

	webhook := false
	if names, err := ghc.SecretNames(); err != nil {
		return err
	} else {
		for _, n := range names {
			webhook = webhook || n == watch.WebhookEnv
		}
	}

	if v := strings.TrimPrefix(Root.Version, "v"); releaseVersion.MatchString(v) {
		watch.ReleaseVersion = v
	}
	content := watch.Workflow(scary, providers, webhook)

	action := "new file"
	if old, err := os.ReadFile(watch.File); err == nil {
		action = "replaces the existing file"
		if string(old) == content {
			action = "unchanged"
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, ui.Bold.Render("  Plan"))
	fmt.Fprintf(out, "    write   %s (%s)\n", watch.File, action)
	for _, s := range secrets {
		fmt.Fprintf(out, "    secret  %s  %s, encrypted by gh\n", s.name, s.what)
	}
	if webhook {
		fmt.Fprintf(out, "    uses    %s  the Slack/Discord webhook already set on this repo\n", watch.WebhookEnv)
	}
	fmt.Fprintf(out, "    save    scary_day_usd = %s in %s\n", fmtUSD(scary), config.File)
	fmt.Fprintln(out)

	if confirmWrite("Show the workflow file first?", false) {
		fmt.Fprintln(out)
		for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
			fmt.Fprintln(out, "    "+l)
		}
		fmt.Fprintln(out)
	}
	if !confirmWrite("Set up the hourly check?", false) {
		fmt.Fprintln(out, ui.Receipt(nil, nil))
		return nil
	}

	var changed []string
	for _, s := range secrets {
		if err := ghc.SecretSet(s.name, s.value); err != nil {
			fmt.Fprintln(out, ui.Receipt([]string{"github"}, changed))
			return err
		}
		changed = append(changed, "secret "+s.name)
	}
	if err := os.MkdirAll(filepath.Dir(watch.File), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(watch.File, []byte(content), 0o644); err != nil {
		return err
	}
	changed = append(changed, watch.File)
	cfg.ScaryDay = scary
	if err := cfg.Save("."); err != nil {
		return err
	}
	changed = append(changed, config.File)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "  "+ui.Green.Render("Ready.")+" Commit and push the workflow to start watching:")
	fmt.Fprintln(out, ui.Cmd.Render("    git add "+watch.File+" "+config.File))
	fmt.Fprintln(out, ui.Cmd.Render(`    git commit -m "Watch AI key spend with keywatch"`))
	fmt.Fprintln(out, ui.Cmd.Render("    git push"))
	fmt.Fprintln(out, "  Then run it once to check it works: "+ui.Cmd.Render("gh workflow run keywatch.yml"))
	if !webhook {
		fmt.Fprintln(out, "  Optional, alerts in chat too: "+ui.Cmd.Render("keywatch alerts slack <webhook-url>"))
	}
	fmt.Fprintln(out, ui.Receipt([]string{"keychain", "github"}, changed))
	return nil
}

func runWatchRun(cmd *cobra.Command, args []string) error {
	if Verbose {
		alert.Logf = func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  "+f+"\n", a...) }
	}
	vars := map[string]string{}
	if s := os.Getenv(watch.VarsEnv); s != "" {
		if err := json.Unmarshal([]byte(s), &vars); err != nil {
			return fmt.Errorf("reading %s: %w", watch.VarsEnv, err)
		}
	}
	scary := watchRunScaryDay
	if !cmd.Flags().Changed("scary-day") {
		if cfg, err := config.Load("."); err == nil {
			scary = cfg.ScaryDay
		}
	}
	return watch.Run(cmd.Context(), watch.Options{
		ScaryDayUSD: scary,
		Vars:        vars,
		Webhook:     os.Getenv(watch.WebhookEnv),
		Out:         cmd.OutOrStdout(),
	})
}

func fmtUSD(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// workflowNote tells the user what to do next if the workflow file doesn't
// pass the webhook to the job yet.
func workflowNote(w io.Writer) {
	b, err := os.ReadFile(watch.File)
	switch {
	case err != nil:
		fmt.Fprintln(w, "  Next, set up the hourly check: "+ui.Cmd.Render("keywatch watch"))
	case !bytes.Contains(b, []byte(watch.WebhookEnv)):
		fmt.Fprintln(w, "  Run "+ui.Cmd.Render("keywatch watch")+" again so the workflow passes the webhook to the job.")
	}
}
