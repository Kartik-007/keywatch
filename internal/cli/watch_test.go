package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Kartik-007/keywatch/internal/config"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/source"
	"github.com/Kartik-007/keywatch/internal/watch"
)

// ghRecorder stands in for the gh CLI.
type ghRecorder struct {
	secrets map[string]string
	vars    map[string]string
	names   []string
	err     error
}

func newGHRecorder(t *testing.T) *ghRecorder {
	g := &ghRecorder{secrets: map[string]string{}, vars: map[string]string{}}
	old := ghc
	ghc = g
	t.Cleanup(func() { ghc = old })
	return g
}

func (g *ghRecorder) SecretSet(name, value string) error {
	if g.err != nil {
		return g.err
	}
	g.secrets[name] = value
	return nil
}
func (g *ghRecorder) SecretNames() ([]string, error) { return g.names, g.err }
func (g *ghRecorder) VariableSet(name, value string) error {
	if g.err != nil {
		return g.err
	}
	g.vars[name] = value
	return nil
}

// stubPrompts answers confirmWrite with yes in order (then no), and askLine
// with ask.
func stubPrompts(t *testing.T, ask string, yes ...bool) *[]string {
	var asked []string
	oldC, oldA := confirmWrite, askLine
	confirmWrite = func(q string, def bool) bool {
		asked = append(asked, q)
		if len(yes) == 0 {
			return false
		}
		a := yes[0]
		yes = yes[1:]
		return a
	}
	askLine = func(q string) string { asked = append(asked, q); return ask }
	t.Cleanup(func() { confirmWrite, askLine = oldC, oldA })
	return &asked
}

func execWatchCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	Root.SetOut(&buf)
	Root.SetErr(&buf)
	Root.SetArgs(args)
	t.Cleanup(func() { Root.SetOut(nil); Root.SetErr(nil); Root.SetArgs(nil) })
	err := Root.Execute()
	return buf.String(), err
}

// watchTestProvider is a connected OpenAI with no usage.
type watchTestProvider struct{ listed bool }

func (p *watchTestProvider) Name() string                          { return core.OpenAI }
func (p *watchTestProvider) AdminKeyURL() string                   { return "" }
func (p *watchTestProvider) Connect(context.Context, string) error { return nil }
func (p *watchTestProvider) ListKeys(context.Context, string) ([]core.ProviderKey, error) {
	p.listed = true
	return nil, nil
}
func (p *watchTestProvider) Usage(context.Context, string, time.Time, time.Time, string) ([]core.UsagePoint, error) {
	return nil, nil
}
func (p *watchTestProvider) Uncapped(context.Context, string) ([]string, error) { return nil, nil }
func (p *watchTestProvider) Disable(context.Context, string, core.ProviderKey) error {
	return errors.New("not in tests")
}
func (p *watchTestProvider) Reversible() bool { return false }

func watchRepo(t *testing.T) *watchTestProvider {
	t.Helper()
	dir := t.TempDir()
	os.Mkdir(dir+"/.git", 0o755)
	t.Chdir(dir)
	p := &watchTestProvider{}
	saved := provider.All()
	provider.Reset(p)
	t.Cleanup(func() { provider.Reset(saved...) })
	t.Setenv("KEYWATCH_OPENAI_ADMIN_KEY", "test-admin")
	return p
}

func TestWatchSetupWritesWorkflowAndSecrets(t *testing.T) {
	watchRepo(t)
	g := newGHRecorder(t)
	asked := stubPrompts(t, "$75", true, true) // show file, then set up

	out, err := execWatchCmd(t, "watch")
	if err != nil {
		t.Fatal(err, out)
	}
	if len(*asked) != 3 || !strings.Contains((*asked)[0], "scary amount for one day") {
		t.Errorf("prompts = %q", *asked)
	}
	want := watch.Workflow(75, []string{core.OpenAI}, false)
	got, err := os.ReadFile(watch.File)
	if err != nil || string(got) != want {
		t.Errorf("workflow file:\n%s\nerr %v", got, err)
	}
	if !strings.Contains(out, "secrets.KEYWATCH_OPENAI_ADMIN_KEY") {
		t.Error("asked to show the file, but it wasn't printed")
	}
	if g.secrets["KEYWATCH_OPENAI_ADMIN_KEY"] != "test-admin" || len(g.secrets) != 1 {
		t.Errorf("secrets = %v", g.secrets)
	}
	if cfg, _ := config.Load("."); cfg.ScaryDay != 75 {
		t.Errorf("scary day saved = %v", cfg.ScaryDay)
	}
	if strings.Contains(out, "test-admin") {
		t.Error("admin key printed")
	}
	for _, s := range []string{"git push", "KEYWATCH_OPENAI_ADMIN_KEY", "Changed:"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
}

func TestWatchSetupUsesExistingWebhook(t *testing.T) {
	watchRepo(t)
	g := newGHRecorder(t)
	g.names = []string{"OTHER", watch.WebhookEnv}
	stubPrompts(t, "", false, true)
	if out, err := execWatchCmd(t, "watch"); err != nil {
		t.Fatal(err, out)
	}
	got, _ := os.ReadFile(watch.File)
	if string(got) != watch.Workflow(50, []string{core.OpenAI}, true) {
		t.Errorf("want default $50 with webhook, got:\n%s", got)
	}
}

func TestWatchSetupDeclinedChangesNothing(t *testing.T) {
	watchRepo(t)
	g := newGHRecorder(t)
	stubPrompts(t, "")
	out, err := execWatchCmd(t, "watch")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(watch.File); err == nil || len(g.secrets) > 0 {
		t.Error("declined setup still wrote something")
	}
	if _, err := os.Stat(config.File); err == nil {
		t.Error("declined setup saved config")
	}
	if !strings.Contains(out, "Nothing was changed") {
		t.Errorf("out:\n%s", out)
	}
}

func TestWatchSetupNeedsConnectedProvider(t *testing.T) {
	watchRepo(t)
	provider.Reset()
	newGHRecorder(t)
	stubPrompts(t, "")
	if _, err := execWatchCmd(t, "watch"); err == nil || !strings.Contains(err.Error(), "keywatch connect") {
		t.Errorf("err = %v", err)
	}
}

func TestWatchSetupBadAmount(t *testing.T) {
	watchRepo(t)
	newGHRecorder(t)
	stubPrompts(t, "lots", true, true)
	if _, err := execWatchCmd(t, "watch"); err == nil {
		t.Error("want error for a non-number")
	}
}

func TestWatchRunReadsVarsFromEnv(t *testing.T) {
	p := watchRepo(t)
	savedSources := source.All()
	source.Reset()
	t.Cleanup(func() { source.Reset(savedSources...) })
	t.Setenv(watch.VarsEnv, `{"KEYWATCH_ACK_A91F":"2030-01-01T00:00:00Z"}`)
	out, err := execWatchCmd(t, "watch", "run", "--scary-day", "50")
	if err != nil || !strings.Contains(out, "All quiet") || !p.listed {
		t.Errorf("err=%v listed=%v out:\n%s", err, p.listed, out)
	}

	t.Setenv(watch.VarsEnv, `not json`)
	if _, err := execWatchCmd(t, "watch", "run"); err == nil {
		t.Error("want error for bad vars JSON")
	}
}
