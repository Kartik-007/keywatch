package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Kartik-007/keywatch/internal/anomaly"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
)

var hour = time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)

var supportBot = core.Key{
	ID: "a91f", Label: "support-bot", Provider: core.OpenAI, Redacted: "sk-…a91f",
	Locations: []core.Location{
		{Source: "railway", Where: "support-bot · production", Var: "OPENAI_API_KEY"},
		{Source: "github", Where: "acme/support-bot", Var: "OPENAI_API_KEY", NameOnly: true},
	},
}

func TestRenderSpike(t *testing.T) {
	provider.Reset()
	a := anomaly.Alert{KeyID: "a91f", Reason: anomaly.Spike, HourStart: hour,
		LastHourUSD: 42.1, NormalHourUSD: 4.5, LimitUSD: 13.5, TodayUSD: 88}
	want := `keywatch alert: support-bot spent $42.10 in the last hour. Normal is up to $4.50/hour.

Key:       support-bot · openai · sk-…a91f
Lives in:  railway: support-bot · production (OPENAI_API_KEY)
           github: acme/support-bot (OPENAI_API_KEY, matched by name)
Normal:    up to $4.50/hour in 95% of hours over the last 14 days
Now:       $42.10 in the hour from 13:00 UTC, $88.00 so far today

Expected? Silence this key for 6 hours:
  keywatch ack a91f 6h
Not expected? Turn the key off:
  keywatch disable a91f
  This breaks: support-bot · production (railway), acme/support-bot (github).
  This deletes the key at the provider. It can't be undone.
`
	if got := Render(a, supportBot); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderNewAnthropicKeyNoLocations(t *testing.T) {
	provider.Reset()
	k := core.Key{ID: "77c2", Label: "batch-jobs", Provider: core.Anthropic}
	a := anomaly.Alert{KeyID: "77c2", Reason: anomaly.Spike, HourStart: hour, LastHourUSD: 9, LimitUSD: 5, TodayUSD: 9, Approx: true}
	got := Render(a, k)
	for _, s := range []string{
		"batch-jobs spent ≈$9.00 in the last hour. Normal is no spend in most hours.",
		"Lives in:  nowhere keywatch can read from here",
		"keywatch ack 77c2 6h",
		"keywatch disable 77c2",
		"This breaks anything using the key.",
		"keywatch enable 77c2",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
}

func TestRenderUnknownKey(t *testing.T) {
	a := anomaly.Alert{KeyID: "key_abc", Reason: anomaly.Spike, HourStart: hour, LastHourUSD: 9, LimitUSD: 5}
	got := Render(a, core.Key{})
	if !strings.Contains(got, "key_abc spent $9.00") || !strings.Contains(got, "keywatch disable key_abc") {
		t.Errorf("want key id fallback, got:\n%s", got)
	}
}

func TestRenderScaryDay(t *testing.T) {
	provider.Reset()
	a := anomaly.Alert{KeyID: anomaly.Org, Reason: anomaly.ScaryDay, HourStart: hour,
		LimitUSD: 50, TodayUSD: 63.2, TopKeyID: "a91f", TopKeyUSD: 40.1}
	got := Render(a, supportBot)
	for _, s := range []string{
		"keywatch alert: AI spend today is $63.20, over your scary-day amount of $50.00.",
		"Biggest spender:  support-bot · openai · sk-…a91f ($40.10 today)",
		"railway: support-bot · production",
		"keywatch ack org 6h",
		"keywatch disable a91f",
		"This breaks: support-bot · production (railway)",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// capture swaps Client for one that records requests and answers status.
func capture(t *testing.T, status int) *[]*http.Request {
	t.Helper()
	var reqs []*http.Request
	old := Client
	Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		reqs = append(reqs, r)
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { Client = old })
	return &reqs
}

// Webhook URLs are built at runtime and kept short so nothing secret-shaped
// is committed.
var (
	slackURL   = "https://hooks.slack.com" + "/services/" + "TEST/TEST/test"
	discordURL = "https://discord.com" + "/api/webhooks/" + "1/test"
)

func TestPostSlack(t *testing.T) {
	reqs := capture(t, 200)
	if err := Post(context.Background(), slackURL, "hello"); err != nil {
		t.Fatal(err)
	}
	if len(*reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(*reqs))
	}
	r := (*reqs)[0]
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	if r.Method != "POST" || r.URL.Host != "hooks.slack.com" || body["text"] != "```\nhello\n```" {
		t.Errorf("unexpected request %s %s %v", r.Method, r.URL.Host, body)
	}
}

func TestPostDiscord(t *testing.T) {
	reqs := capture(t, 204)
	if err := Post(context.Background(), discordURL, strings.Repeat("x", 3000)); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Content string `json:"content"`
		Allowed struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	json.NewDecoder((*reqs)[0].Body).Decode(&body)
	if n := len([]rune(body.Content)); n != discordMax {
		t.Errorf("content is %d runes, want truncated to %d", n, discordMax)
	}
	if body.Allowed.Parse == nil {
		t.Error("want allowed_mentions.parse = [] so alerts never ping")
	}
}

func TestPostErrors(t *testing.T) {
	reqs := capture(t, 404)
	err := Post(context.Background(), slackURL, "hi")
	if err == nil || !strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "TEST") {
		t.Errorf("want status error without the URL, got %v", err)
	}
	for _, bad := range []string{
		"http://hooks.slack.com/services/x",
		"https://example.com/services/x",
		"https://hooks.slack.com.evil.test/services/x",
		"https://hooks.slack.com/other",
		"https://discord.com/channels/1",
		"https://hooks.slack.com:8443/services/x",
		"not a url",
	} {
		if err := Post(context.Background(), bad, "hi"); err == nil {
			t.Errorf("%s: want refused", bad)
		}
	}
	if len(*reqs) != 1 {
		t.Errorf("refused URLs must not be contacted; got %d requests", len(*reqs))
	}
}

func TestPostLogsWithoutSecretPath(t *testing.T) {
	capture(t, 200)
	var logged []string
	Logf = func(f string, a ...any) { logged = append(logged, f) }
	defer func() { Logf = nil }()
	Post(context.Background(), slackURL, "hi")
	if len(logged) != 1 || strings.Contains(logged[0], "TEST") {
		t.Errorf("want one log line without the secret path, got %q", logged)
	}
}

func TestKindOf(t *testing.T) {
	if k, err := KindOf(slackURL); err != nil || k != Slack {
		t.Errorf("slack: %v %v", k, err)
	}
	if k, err := KindOf(discordURL); err != nil || k != Discord {
		t.Errorf("discord: %v %v", k, err)
	}
}
