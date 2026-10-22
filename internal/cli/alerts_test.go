package cli

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Kartik-007/keywatch/internal/alert"
	"github.com/Kartik-007/keywatch/internal/watch"
)

// Built at runtime and kept short, so nothing webhook-shaped is committed.
var (
	testSlackHook   = "https://hooks.slack.com" + "/services/" + "TEST/TEST/test"
	testDiscordHook = "https://discord.com" + "/api/webhooks/" + "1/test"
)

type alertsRT func(*http.Request) (*http.Response, error)

func (f alertsRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func captureWebhook(t *testing.T) *[]string {
	var hosts []string
	old := alert.Client
	alert.Client = &http.Client{Transport: alertsRT(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { alert.Client = old })
	return &hosts
}

func TestAlertsSlackSetsSecret(t *testing.T) {
	t.Chdir(t.TempDir())
	g := newGHRecorder(t)
	posts := captureWebhook(t)
	stubPrompts(t, "", true, true) // set it, send a test

	out, err := execWatchCmd(t, "alerts", "slack", testSlackHook)
	if err != nil {
		t.Fatal(err, out)
	}
	if g.secrets[watch.WebhookEnv] != testSlackHook {
		t.Errorf("secrets = %v", g.secrets)
	}
	if len(*posts) != 1 || (*posts)[0] != "hooks.slack.com" {
		t.Errorf("posts = %v", *posts)
	}
	if strings.Contains(out, "TEST/TEST") {
		t.Error("webhook URL printed")
	}
	if !strings.Contains(out, "keywatch watch") {
		t.Errorf("want a pointer to keywatch watch, got:\n%s", out)
	}
}

func TestAlertsWorkflowNeedsUpdate(t *testing.T) {
	t.Chdir(t.TempDir())
	os.MkdirAll(".github/workflows", 0o755)
	os.WriteFile(watch.File, []byte(watch.Workflow(50, []string{"openai"}, false)), 0o644)
	newGHRecorder(t)
	stubPrompts(t, "", true, false)
	out, err := execWatchCmd(t, "alerts", "discord", testDiscordHook)
	if err != nil || !strings.Contains(out, "again so the workflow passes the webhook") {
		t.Errorf("err=%v out:\n%s", err, out)
	}
}

func TestAlertsDeclinedOrWrongKind(t *testing.T) {
	t.Chdir(t.TempDir())
	g := newGHRecorder(t)
	stubPrompts(t, "")
	if _, err := execWatchCmd(t, "alerts", "slack", testSlackHook); err != nil || len(g.secrets) > 0 {
		t.Errorf("declined: err=%v secrets=%v", err, g.secrets)
	}
	stubPrompts(t, "", true)
	if _, err := execWatchCmd(t, "alerts", "slack", testDiscordHook); err == nil || len(g.secrets) > 0 {
		t.Errorf("discord URL to slack: err=%v secrets=%v", err, g.secrets)
	}
	stubPrompts(t, "", true)
	if _, err := execWatchCmd(t, "alerts", "slack", "https://example.com/services/x"); err == nil || len(g.secrets) > 0 {
		t.Errorf("other host: err=%v secrets=%v", err, g.secrets)
	}
}

func TestAlertsTest(t *testing.T) {
	posts := captureWebhook(t)
	t.Setenv(watch.WebhookEnv, "")
	out, err := execWatchCmd(t, "alerts", "test")
	if err != nil || len(*posts) != 0 || !strings.Contains(out, "keywatch ack a91f 6h") || !strings.Contains(out, "pass the URL") {
		t.Errorf("no URL: err=%v posts=%v out:\n%s", err, *posts, out)
	}
	out, err = execWatchCmd(t, "alerts", "test", testDiscordHook)
	if err != nil || len(*posts) != 1 || (*posts)[0] != "discord.com" || !strings.Contains(out, "Sent a sample alert to Discord") {
		t.Errorf("with URL: err=%v posts=%v out:\n%s", err, *posts, out)
	}
}
