package watch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Kartik-007/keywatch/internal/alert"
	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/source"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

var runNow = time.Date(2026, 10, 21, 14, 20, 0, 0, time.UTC)

// fakeProvider is an OpenAI stand-in: one listed key and canned hourly usage.
type fakeProvider struct {
	usage    []core.UsagePoint
	usageErr error
	calls    []string
}

func (f *fakeProvider) Name() string                                       { return core.OpenAI }
func (f *fakeProvider) AdminKeyURL() string                                { return "" }
func (f *fakeProvider) Connect(context.Context, string) error              { return nil }
func (f *fakeProvider) Uncapped(context.Context, string) ([]string, error) { return nil, nil }
func (f *fakeProvider) Disable(context.Context, string, core.ProviderKey) error {
	return errors.New("not in tests")
}
func (f *fakeProvider) Reversible() bool { return false }
func (f *fakeProvider) ListKeys(context.Context, string) ([]core.ProviderKey, error) {
	return []core.ProviderKey{{Provider: core.OpenAI, ID: "key_1", Name: "prod", Hint: testutil.OpenAI1Hint, Active: true}}, nil
}
func (f *fakeProvider) Usage(_ context.Context, ak string, from, to time.Time, bucket string) ([]core.UsagePoint, error) {
	f.calls = append(f.calls, ak+" "+bucket+" "+from.Format(time.RFC3339)+" "+to.Format(time.RFC3339))
	return f.usage, f.usageErr
}

type fakeSource struct{ found []core.FoundSecret }

func (s fakeSource) Name() string    { return "railway" }
func (s fakeSource) Available() bool { return true }
func (s fakeSource) Collect(context.Context) ([]core.FoundSecret, []string, error) {
	return s.found, nil, nil
}

// spiky is $1/hour for 14 days, then $40 in the last complete hour.
func spiky(id string) []core.UsagePoint {
	last := runNow.Truncate(time.Hour).Add(-time.Hour)
	var pts []core.UsagePoint
	for h := 1; h <= 14*24; h++ {
		pts = append(pts, core.UsagePoint{KeyID: id, Start: last.Add(-time.Duration(h) * time.Hour), USD: 1})
	}
	return append(pts, core.UsagePoint{KeyID: id, Start: last, USD: 40})
}

func setup(t *testing.T, usage []core.UsagePoint) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{usage: usage}
	provider.Reset(fp)
	source.Reset(fakeSource{found: []core.FoundSecret{{
		Provider: core.OpenAI, Value: testutil.OpenAI1,
		Location: core.Location{Source: "railway", Where: "support-bot · production", Var: "OPENAI_API_KEY"},
	}}})
	t.Cleanup(func() { provider.Reset(); source.Reset() })
	t.Setenv("KEYWATCH_OPENAI_ADMIN_KEY", "test-admin")
	return fp
}

func TestRunAlertsWithLabelsAndLocations(t *testing.T) {
	fp := setup(t, spiky("key_1"))
	var out bytes.Buffer
	err := Run(context.Background(), Options{Now: runNow, ScaryDayUSD: 500, Out: &out, Dir: t.TempDir()})
	if !errors.Is(err, ErrAlerts) {
		t.Fatalf("want ErrAlerts, got %v", err)
	}
	got := out.String()
	for _, s := range []string{
		"support-bot · production spent $40.00 in the last hour",
		"railway: support-bot · production (OPENAI_API_KEY)",
		"keywatch ack a91f 6h",
		"keywatch disable a91f",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
	if strings.Contains(got, testutil.OpenAI1) {
		t.Fatal("full key printed")
	}
	want := "test-admin 1h 2026-10-06T00:00:00Z 2026-10-21T15:00:00Z"
	if len(fp.calls) != 1 || fp.calls[0] != want {
		t.Errorf("usage calls = %q, want %q", fp.calls, want)
	}
}

func TestRunUnmatchedKeyFallsBackToProviderID(t *testing.T) {
	setup(t, spiky("key_other"))
	var out bytes.Buffer
	err := Run(context.Background(), Options{Now: runNow, Out: &out, Dir: t.TempDir()})
	if !errors.Is(err, ErrAlerts) || !strings.Contains(out.String(), "keywatch disable key_other") {
		t.Fatalf("err=%v out:\n%s", err, out.String())
	}
}

func TestRunAckSilences(t *testing.T) {
	setup(t, spiky("key_1"))
	var out bytes.Buffer
	vars := map[string]string{"KEYWATCH_ACK_A91F": runNow.Add(time.Hour).Format(time.RFC3339), "OTHER": "x"}
	if err := Run(context.Background(), Options{Now: runNow, Vars: vars, Out: &out, Dir: t.TempDir()}); err != nil {
		t.Fatalf("want quiet, got %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "a91f is acked until") || !strings.Contains(out.String(), "All quiet") {
		t.Errorf("out:\n%s", out.String())
	}
}

func TestRunExpiredAckAlerts(t *testing.T) {
	setup(t, spiky("key_1"))
	vars := map[string]string{"KEYWATCH_ACK_A91F": runNow.Add(-time.Hour).Format(time.RFC3339)}
	err := Run(context.Background(), Options{Now: runNow, Vars: vars, Out: io.Discard, Dir: t.TempDir()})
	if !errors.Is(err, ErrAlerts) {
		t.Fatalf("want ErrAlerts, got %v", err)
	}
}

func TestRunQuiet(t *testing.T) {
	last := runNow.Truncate(time.Hour).Add(-time.Hour)
	setup(t, []core.UsagePoint{{KeyID: "key_1", Start: last, USD: 2}})
	var out bytes.Buffer
	if err := Run(context.Background(), Options{Now: runNow, ScaryDayUSD: 50, Out: &out, Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "All quiet. Checked 1 keys from openai") {
		t.Errorf("out:\n%s", out.String())
	}
}

func TestRunPostsToWebhook(t *testing.T) {
	setup(t, spiky("key_1"))
	var posted []string
	old := alert.Client
	alert.Client = &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		posted = append(posted, r.URL.Host+" "+string(b))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { alert.Client = old })

	hook := "https://hooks.slack.com" + "/services/" + "TEST/TEST/test"
	Run(context.Background(), Options{Now: runNow, Webhook: hook, Out: io.Discard, Dir: t.TempDir()})
	if len(posted) != 1 || !strings.HasPrefix(posted[0], "hooks.slack.com ") || !strings.Contains(posted[0], "keywatch ack a91f 6h") {
		t.Errorf("posted = %q", posted)
	}
}

func TestRunProviderErrorFails(t *testing.T) {
	fp := setup(t, nil)
	fp.usageErr = errors.New("503 from provider")
	var out bytes.Buffer
	err := Run(context.Background(), Options{Now: runNow, Out: &out, Dir: t.TempDir()})
	if err == nil || errors.Is(err, ErrAlerts) || !strings.Contains(err.Error(), "openai") {
		t.Fatalf("want usage error, got %v", err)
	}
}

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
