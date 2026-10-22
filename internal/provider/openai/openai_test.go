package openai

import (
	"context"
	"errors"
	"fmt"
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

var openAI2Hint = "sk-" + "proj-key...3e0b"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "openai", name))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.NewReplacer("{{OPENAI_1_HINT}}", testutil.OpenAI1Hint, "{{OPENAI_2_HINT}}", openAI2Hint).Replace(string(b))
	return []byte(s)
}

type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, q := range r.reqs {
		out = append(out, q.Method+" "+q.URL.Path)
	}
	return out
}

// serve routes method+path (plus a query param when needed) to fixtures.
func serve(t *testing.T, route func(r *http.Request) (int, string)) *recorder {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, r)
		rec.mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer "+adminKey {
			t.Errorf("bad auth header on %s", r.URL.Path)
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

func TestListKeysPaginatesProjects(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/organization/projects":
			if r.URL.Query().Get("after") == "proj_support" {
				return 200, "projects_page2.json"
			}
			return 200, "projects_page1.json"
		case "/v1/organization/projects/proj_support/api_keys":
			if r.URL.Query().Get("owner_project_access") != "any" {
				t.Error("want owner_project_access=any")
			}
			return 200, "api_keys_proj_support.json"
		case "/v1/organization/projects/proj_research/api_keys":
			return 200, "api_keys_proj_research.json"
		}
		t.Errorf("unexpected %s", r.URL.Path)
		return 404, ""
	})
	keys, err := Provider{}.ListKeys(context.Background(), adminKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d", len(keys))
	}
	k := keys[0]
	if k.ID != "key_support01" || k.Project != "proj_support" || !k.Active || k.Provider != core.OpenAI {
		t.Fatalf("got %+v", k)
	}
	if !core.MatchesHint(testutil.OpenAI1, k.Hint) {
		t.Fatal("hint should match the fake key")
	}
	if k.LastUsed.IsZero() || k.CreatedAt.Year() != 2025 {
		t.Fatalf("times: %+v", k)
	}
	if !keys[1].LastUsed.IsZero() || keys[1].Project != "proj_research" {
		t.Fatalf("got %+v", keys[1])
	}
}

func TestUsageDailyUsesExactCosts(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/organization/costs" {
			t.Errorf("unexpected %s", r.URL.Path)
			return 404, ""
		}
		q := r.URL.Query()
		if q.Get("group_by[]") != "api_key_id" || q.Get("bucket_width") != "1d" {
			t.Errorf("query %v", q)
		}
		if q.Get("page") != "" {
			return 200, "costs_page2.json"
		}
		return 200, "costs_page1.json"
	})
	from := time.Unix(1792540800, 0)
	pts, err := Provider{}.Usage(context.Background(), adminKey, from, from.AddDate(0, 0, 2), "1d")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(rec.seen()); n != 2 {
		t.Fatalf("want 2 pages fetched, got %d", n)
	}
	// Costs with no api_key_id are dropped; 3 keyed results remain.
	if len(pts) != 3 {
		t.Fatalf("got %+v", pts)
	}
	var total float64
	for _, p := range pts {
		if p.Approx {
			t.Fatal("daily costs are exact")
		}
		total += p.USD
	}
	if math.Abs(total-16.15) > 1e-9 {
		t.Fatalf("total %v", total)
	}
}

func TestUsageHourlyUsesTokensTimesPrice(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) {
		if r.URL.Path != "/v1/organization/usage/completions" {
			t.Errorf("unexpected %s", r.URL.Path)
			return 404, ""
		}
		q := r.URL.Query()
		if got := strings.Join(q["group_by[]"], ","); got != "api_key_id,model,batch" {
			t.Errorf("group_by %q", got)
		}
		if q.Get("bucket_width") != "1h" || q.Get("limit") != "168" {
			t.Errorf("query %v", q)
		}
		return 200, "usage_completions_1h.json"
	})
	from := time.Unix(1792623600, 0)
	pts, err := Provider{}.Usage(context.Background(), adminKey, from, from.Add(time.Hour), "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 1 || !pts[0].Approx || pts[0].KeyID != "key_support01" {
		t.Fatalf("got %+v", pts)
	}
	// gpt-6-sol: 600k×$2 + 400k cached×$0.20 + 100k out×$10 = $2.28
	// gpt-4o-mini batch: 2M×$0.15×0.5 = $0.15
	if math.Abs(pts[0].USD-2.43) > 1e-9 {
		t.Fatalf("usd %v", pts[0].USD)
	}
}

func TestUsageHourlyRespectsBucketLimit(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) { return 200, "usage_completions_1h.json" })
	from := time.Unix(1792623600, 0)
	if _, err := (Provider{}).Usage(context.Background(), adminKey, from, from.Add(200*time.Hour), "1h"); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.seen()); n != 2 {
		t.Fatalf("200 hours should take 2 requests of ≤168 buckets, got %d", n)
	}
}

func TestUsageRejectsUnknownBucket(t *testing.T) {
	if _, err := (Provider{}).Usage(context.Background(), adminKey, time.Now(), time.Now(), "1m"); err == nil {
		t.Fatal("want error")
	}
}

func TestConnect401ExplainsAdminKey(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 401, "error_401.json" })
	err := Provider{}.Connect(context.Background(), adminKey)
	if err == nil || !strings.Contains(err.Error(), "Admin key") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("got %v", err)
	}
}

func TestConnectOK(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("limit") != "1" {
			t.Error("connect should be one cheap call")
		}
		return 200, "projects_page2.json"
	})
	if err := (Provider{}).Connect(context.Background(), adminKey); err != nil {
		t.Fatal(err)
	}
}

func TestDisableDeletesKey(t *testing.T) {
	rec := serve(t, func(r *http.Request) (int, string) { return 200, "delete_key.json" })
	k := core.ProviderKey{ID: "key_support01", Project: "proj_support"}
	if err := (Provider{}).Disable(context.Background(), adminKey, k); err != nil {
		t.Fatal(err)
	}
	if got := rec.seen(); len(got) != 1 || got[0] != "DELETE /v1/organization/projects/proj_support/api_keys/key_support01" {
		t.Fatalf("got %v", got)
	}
	if (Provider{}).Reversible() {
		t.Fatal("delete is irreversible")
	}
}

func TestDisableForbiddenExplainsReadOnly(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 403, "error_401.json" })
	err := Provider{}.Disable(context.Background(), adminKey, core.ProviderKey{ID: "k", Project: "p"})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("got %v", err)
	}
}

func TestUncapped(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v1/organization/spend_limit":
			return 404, "error_404_spend_limit.json"
		case "/v1/organization/projects":
			if r.URL.Query().Get("after") == "proj_support" {
				return 200, "projects_page2.json"
			}
			return 200, "projects_page1.json"
		case "/v1/organization/projects/proj_support/spend_limit":
			return 200, "spend_limit_project.json"
		case "/v1/organization/projects/proj_research/spend_limit":
			return 404, "error_404_spend_limit.json"
		}
		return 500, ""
	})
	got, err := Provider{}.Uncapped(context.Background(), adminKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "proj_research" {
		t.Fatalf("got %v", got)
	}
}

func TestUncappedOrgLimitCapsEverything(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 200, "spend_limit_project.json" })
	got, err := Provider{}.Uncapped(context.Background(), adminKey)
	if err != nil || got != nil {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestErrorsAreAPIError(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 500, "" })
	_, err := Provider{}.ListKeys(context.Background(), adminKey)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 500 {
		t.Fatalf("got %v", err)
	}
}

func TestLogfNeverSeesQueryOrKey(t *testing.T) {
	serve(t, func(r *http.Request) (int, string) { return 200, "projects_page2.json" })
	var lines []string
	old := Logf
	Logf = func(f string, a ...any) { lines = append(lines, strings.TrimSpace(fmt.Sprintf(f, a...))) }
	defer func() { Logf = old }()
	Provider{}.Connect(context.Background(), adminKey)
	if len(lines) != 1 || strings.Contains(lines[0], "?") || strings.Contains(lines[0], adminKey) || !strings.HasPrefix(lines[0], "GET ") {
		t.Fatalf("got %v", lines)
	}
}

func TestPriceFor(t *testing.T) {
	cases := map[string]float64{
		"gpt-4o-mini-2024-07-18": 0.15,
		"gpt-4o-2024-08-06":      2.50,
		"gpt-4o-2024-05-13":      5,
		"gpt-5.4-mini":           0.75,
		"ft:gpt-4.1-mini:acme":   0.40,
		"some-future-model":      fallback.In,
	}
	for m, want := range cases {
		if got := priceFor(m).In; got != want {
			t.Errorf("%s: got %v want %v", m, got, want)
		}
	}
}
