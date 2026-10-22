package railway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

const testToken = "railway-test-token"

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "railway", name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(testutil.Fill(string(b)))
}

// setup logs in with a fake CLI config and serves recorded responses. Reads
// listed in fail return HTTP 500.
func setup(t *testing.T, fail ...string) {
	t.Helper()
	t.Setenv("RAILWAY_API_TOKEN", "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(`{"user":{"token":"`+testToken+`"},"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldPath, oldEndpoint := configPath, Endpoint
	configPath = func() string { return cfg }

	var vars map[string]json.RawMessage
	if err := json.Unmarshal(testdata(t, "variables.json"), &vars); err != nil {
		t.Fatal(err)
	}
	projects := testdata(t, "projects.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "keywatchProjects"):
			w.Write(projects)
		case strings.Contains(req.Query, "keywatchVariables"):
			key := req.Variables["environmentId"] + "/" + req.Variables["serviceId"]
			for _, f := range fail {
				if f == key {
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"errors":[{"message":"Problem processing request"}]}`))
					return
				}
			}
			v, ok := vars[key]
			if !ok {
				t.Errorf("unexpected variables read %q", key)
				v = json.RawMessage(`{}`)
			}
			fmt.Fprintf(w, `{"data":{"variables":%s}}`, v)
		default:
			t.Errorf("unexpected query %q", req.Query)
		}
	}))
	Endpoint = srv.URL
	t.Cleanup(func() {
		srv.Close()
		configPath, Endpoint = oldPath, oldEndpoint
	})
}

// summary renders secrets without ever including a full value.
func summary(found []core.FoundSecret) []string {
	var out []string
	for _, f := range found {
		v := core.Last4(f.Value)
		if f.Location.NameOnly {
			v = "name-only"
		}
		out = append(out, fmt.Sprintf("%s | %s | %s | %s | %s", f.Location.Source, f.Location.Where, f.Location.Var, f.Provider, v))
	}
	sort.Strings(out)
	return out
}

func TestAvailable(t *testing.T) {
	t.Setenv("RAILWAY_API_TOKEN", "")
	old := configPath
	t.Cleanup(func() { configPath = old })

	dir := t.TempDir()
	configPath = func() string { return filepath.Join(dir, "missing.json") }
	if (Railway{}).Available() {
		t.Fatal("available without a login")
	}
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte(`{"user":{"accessToken":"a","token":"b"}}`), 0o600)
	configPath = func() string { return cfg }
	if !(Railway{}).Available() || token() != "a" {
		t.Fatal("OAuth access token should win over the legacy token")
	}
}

func TestCollect(t *testing.T) {
	setup(t, "e-stg/s-api")
	found, notes, err := Railway{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"railway | landing / shared · production | SHARED_OPENAI_FALLBACK | openai | name-only",
		"railway | landing / web · production | AGENT_CONFIG | openai | 3e0b",
		"railway | landing / web · production | CLAUDE_KEY | anthropic | 77c2",
		"railway | support-bot / api · production | OPENAI_API_KEY | openai | a91f",
		"railway | support-bot / worker · production | ANTHROPIC_API_KEY | anthropic | name-only",
		"railway | support-bot / worker · production | OPENAI_API_KEY | openai | a91f",
	}
	if got := summary(found); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, f := range found {
		if f.Location.NameOnly && f.Value != "" {
			t.Fatal("name-only secret carries a value")
		}
	}
	wantNotes := []string{
		"Railway sealed variables can't be read back, so 2 were matched by name only.",
		"Skipped 1 of 7 Railway variable sets that couldn't be read: support-bot / api · staging.",
	}
	if strings.Join(notes, "\n") != strings.Join(wantNotes, "\n") {
		t.Fatalf("notes:\n%s", strings.Join(notes, "\n"))
	}
}

func TestCollectEverythingFails(t *testing.T) {
	setup(t, "e-prod/", "e-prod/s-api", "e-prod/s-worker", "e-stg/", "e-stg/s-api", "e-landing-prod/", "e-landing-prod/s-web")
	found, _, err := Railway{}.Collect(context.Background())
	if err == nil || len(found) != 0 {
		t.Fatalf("want an error when nothing could be read, got %v (%d found)", err, len(found))
	}
}

func TestCollectBadLogin(t *testing.T) {
	setup(t)
	t.Setenv("RAILWAY_API_TOKEN", "wrong")
	_, _, err := Railway{}.Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "railway login") {
		t.Fatalf("want a login hint, got %v", err)
	}
}

func TestLogsRequests(t *testing.T) {
	setup(t)
	var lines []string
	old := Logf
	Logf = func(format string, a ...any) { lines = append(lines, fmt.Sprintf(format, a...)) }
	t.Cleanup(func() { Logf = old })
	if _, _, err := (Railway{}).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 8 || !strings.HasPrefix(lines[0], "POST 127.0.0.1:") {
		t.Fatalf("logged %q", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, testToken) {
			t.Fatal("token logged")
		}
	}
}
