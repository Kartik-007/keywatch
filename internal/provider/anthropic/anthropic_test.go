package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

const adminKey = "test-admin-key"

var anthropic2Hint = "sk-" + "ant-api03-key...0ld1"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "anthropic", name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.NewReplacer(
		"{{ANTHROPIC_1_HINT}}", testutil.Anthropic1Hint,
		"{{ANTHROPIC_2_HINT}}", anthropic2Hint,
	).Replace(string(b)))
}

type seenReq struct {
	method, path string
	query        map[string][]string
	body         string
}

type recorder struct {
	mu   sync.Mutex
	reqs []seenReq
}

func (r *recorder) all() []seenReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seenReq(nil), r.reqs...)
}

func serve(t *testing.T, route func(r *http.Request) (int, string)) *recorder {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, seenReq{r.Method, r.URL.Path, r.URL.Query(), string(body)})
		rec.mu.Unlock()
		if r.Header.Get("x-api-key") != adminKey || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("bad headers on %s", r.URL.Path)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "keywatch/") {
			t.Errorf("user agent %q", ua)
		}
		code, name := route(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if name != "" {
			w.Write(fixture(t, name))
		}
	}))
	t.Cleanup(srv.Close)
	old := BaseURL
	BaseURL = srv.URL + "/v1"
	t.Cleanup(func() { BaseURL = old })
	return rec
}

func TestListKeysPaginates(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/organizations/api_keys" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		if r.URL.Query().Get("after_id") == "apikey_01SupportBotProd" {
			return 200, "api_keys_page2.json"
		}
		return 200, "api_keys_page1.json"
	})
	keys, err := Provider{}.ListKeys(context.Background(), adminKey)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(rec.all()); n != 2 {
		t.Fatalf("want 2 pages, got %d", n)
	}
	if len(keys) != 3 {
		t.Fatalf("want 3 keys, got %d", len(keys))
	}
	k := keys[0]
	if k.Provider != core.Anthropic || !k.Active || k.Project != "wrkspc_01SupportBot" || k.CreatedAt.Year() != 2025 {
		t.Fatalf("got %+v", k)
	}
	if !core.MatchesHint(testutil.Anthropic1, k.Hint) {
		t.Fatal("hint should match the fake key")
	}
	if keys[1].Active || keys[1].Project != "wrkspc_01DefaultReal" {
		t.Fatalf("inactive key, scope.workspace_id wins over deprecated workspace_id: %+v", keys[1])
	}
	if keys[2].Hint != "" || keys[2].Project != "" || !keys[2].Active {
		t.Fatalf("org-scoped key with no hint: %+v", keys[2])
	}
}

func TestUsageDailyPaginatesAndPrices(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/organizations/usage_report/messages" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		q := r.URL.Query()
		if got := strings.Join(q["group_by[]"], ","); got != "api_key_id,model,service_tier,speed" {
			t.Errorf("group_by %q", got)
		}
		if q.Get("bucket_width") != "1d" || q.Get("limit") != "31" {
			t.Errorf("query %v", q)
		}
		if q.Get("page") != "" {
			return 200, "usage_1d_page2.json"
		}
		return 200, "usage_1d_page1.json"
	})
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	pts, err := Provider{}.Usage(context.Background(), adminKey, from, from.AddDate(0, 0, 3), "1d")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(rec.all()); n != 2 {
		t.Fatalf("want 2 pages, got %d", n)
	}
	got := map[string]float64{}
	for _, p := range pts {
		if !p.Approx {
			t.Fatal("anthropic spend is always approximate")
		}
		got[p.Start.Format("01-02")+" "+p.KeyID] = p.USD
	}
	want := map[string]float64{
		// sonnet 5.5: 1M in $2 + 1M cache read $0.10 + 100k out $1 = $3.10
		// opus 5.5 batch: 1M in × $4 × 0.5 = $2, + 10 web searches $0.10
		"10-19 apikey_01SupportBotProd": 5.20,
		// opus 5.5 fast: 100k out × $20 × 2 = $4
		"10-20 apikey_01SupportBotProd": 4.00,
		// unknown model → fallback $10 in: 100k = $1, 100k 5m writes × 1.25 = $1.25
		"10-20 apikey_01OldPrototype": 2.25,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if math.Abs(got[k]-v) > 1e-9 {
			t.Errorf("%s: got %v want %v", k, got[k], v)
		}
	}
}

func TestUsageHourlyChunksAt168(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) { return 200, "usage_1d_page2.json" })
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, err := (Provider{}).Usage(context.Background(), adminKey, from, from.Add(200*time.Hour), "1h"); err != nil {
		t.Fatal(err)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("want 2 windows, got %d", len(reqs))
	}
	if reqs[0].query["limit"][0] != "168" || reqs[1].query["starting_at"][0] != "2026-10-08T00:00:00Z" {
		t.Fatalf("got %+v", reqs)
	}
}

func TestUsageRejectsUnknownBucket(t *testing.T) {
	if _, err := (Provider{}).Usage(context.Background(), adminKey, time.Now(), time.Now(), "1w"); err == nil {
		t.Fatal("want error")
	}
}

func TestConnect4xxExplainsOrganization(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 403, "error_403.json" })
	err := Provider{}.Connect(context.Background(), adminKey)
	if err == nil || !strings.Contains(err.Error(), "organization accounts, not individual") {
		t.Fatalf("got %v", err)
	}
}

func TestConnectOK(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) { return 200, "organization_me.json" })
	if err := (Provider{}).Connect(context.Background(), adminKey); err != nil {
		t.Fatal(err)
	}
	if r := rec.all(); len(r) != 1 || r[0].path != "/v1/organizations/me" {
		t.Fatalf("got %+v", r)
	}
}

func TestDisableAndEnable(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) {
		var b struct{ Status string }
		json.NewDecoder(r.Body).Decode(&b)
		if b.Status == "active" {
			return 200, "api_key_active.json"
		}
		return 200, "api_key_inactive.json"
	})
	k := core.ProviderKey{ID: "apikey_01SupportBotProd"}
	if err := (Provider{}).Disable(context.Background(), adminKey, k); err != nil {
		t.Fatal(err)
	}
	if err := Enable(context.Background(), adminKey, k); err != nil {
		t.Fatal(err)
	}
	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("got %+v", reqs)
	}
	for i, want := range []string{`{"status":"inactive"}`, `{"status":"active"}`} {
		r := reqs[i]
		if r.method != "POST" || r.path != "/v1/organizations/api_keys/apikey_01SupportBotProd" || r.body != want {
			t.Errorf("req %d: %+v", i, r)
		}
	}
	if !(Provider{}).Reversible() {
		t.Fatal("anthropic disable is reversible")
	}
}

func TestDisableSurfacesAPIError(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 403, "error_403.json" })
	err := Provider{}.Disable(context.Background(), adminKey, core.ProviderKey{ID: "apikey_x"})
	if ae, ok := err.(*APIError); !ok || ae.Status != 403 || ae.Type != "permission_error" {
		t.Fatalf("got %v", err)
	}
}

func TestUncappedNotExposed(t *testing.T) {
	got, err := Provider{}.Uncapped(context.Background(), adminKey)
	if got != nil || err != nil {
		t.Fatal("anthropic exposes no workspace spend limits")
	}
}

func TestLogfOnlyMethodHostPath(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 200, "api_keys_page2.json" })
	var lines []string
	old := Logf
	Logf = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	defer func() { Logf = old }()
	Provider{}.ListKeys(context.Background(), adminKey)
	if len(lines) != 1 || strings.Contains(lines[0], "?") || strings.Contains(lines[0], adminKey) {
		t.Fatalf("got %v", lines)
	}
}

func TestPriceFor(t *testing.T) {
	cases := map[string]float64{
		"claude-opus-5-5":            4,
		"claude-opus-5":              5,
		"claude-sonnet-4-5-20250929": 3,
		"claude-opus-4-1-20250805":   15,
		"claude-haiku-5-5":           0.10,
		"claude-something-new":       fallback.In,
	}
	for m, want := range cases {
		if got := priceFor(m).In; got != want {
			t.Errorf("%s: got %v want %v", m, got, want)
		}
	}
}
