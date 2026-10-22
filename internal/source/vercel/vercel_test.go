package vercel

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

const testToken = "vercel-test-token"

func responses(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "vercel", "responses.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(testutil.Fill(string(b))), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// setup logs in with a fake auth.json and serves recorded responses, keyed
// by path plus the teamId/until query and "#nodecrypt" when decrypt is off.
// A body with a "status" field is returned with that status code.
func setup(t *testing.T, resp map[string]json.RawMessage) {
	t.Helper()
	dir := t.TempDir()
	auth := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(auth, []byte(`{"// Note":"This is your Vercel credentials file.","token":"`+testToken+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldAuth, oldBase := authPath, BaseURL
	authPath = func() string { return auth }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"code":"forbidden","message":"Not authorized","invalidToken":true}}`))
			return
		}
		key := r.URL.Path
		var qs []string
		for _, p := range []string{"teamId", "until"} {
			if v := r.URL.Query().Get(p); v != "" {
				qs = append(qs, p+"="+v)
			}
		}
		if len(qs) > 0 {
			key += "?" + strings.Join(qs, "&")
		}
		if strings.HasSuffix(r.URL.Path, "/env") && r.URL.Query().Get("decrypt") != "true" {
			key += "#nodecrypt"
		}
		body, ok := resp[key]
		if !ok {
			t.Errorf("unexpected request %q", key)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var status struct {
			Status int `json:"status"`
		}
		json.Unmarshal(body, &status)
		if status.Status != 0 {
			w.WriteHeader(status.Status)
		}
		w.Write(body)
	}))
	BaseURL = srv.URL
	t.Cleanup(func() {
		srv.Close()
		authPath, BaseURL = oldAuth, oldBase
	})
}

// summary renders secrets without ever including a full value.
func summary(found []core.FoundSecret) string {
	var out []string
	for _, f := range found {
		v := core.Last4(f.Value)
		if f.Location.NameOnly {
			v = "name-only"
			if f.Value != "" {
				v = "name-only WITH VALUE"
			}
		}
		out = append(out, fmt.Sprintf("%s | %s | %s | %s | %s", f.Location.Source, f.Location.Where, f.Location.Var, f.Provider, v))
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestAvailable(t *testing.T) {
	old := authPath
	t.Cleanup(func() { authPath = old })
	dir := t.TempDir()
	authPath = func() string { return filepath.Join(dir, "auth.json") }
	if (Vercel{}).Available() {
		t.Fatal("available without a login")
	}
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"token":"x"}`), 0o600)
	if !(Vercel{}).Available() {
		t.Fatal("not available with a login")
	}
}

func TestCollect(t *testing.T) {
	setup(t, responses(t))
	found, notes, err := Vercel{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"vercel | landing-chat · production | ANTHROPIC_API_KEY | anthropic | name-only",
		"vercel | landing-chat · production, preview | OPENAI_API_KEY | openai | a91f",
		"vercel | support-agent · custom environment | MODEL_KEY | openai | 3e0b",
		"vercel | support-agent · production | CLAUDE_API_KEY | anthropic | 77c2",
	}, "\n")
	if got := summary(found); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	wantNotes := strings.Join([]string{
		"Vercel sensitive variables can't be read back, so 1 was matched by name only.",
		"Skipped 1 of 4 Vercel projects that couldn't be read: broken-app.",
	}, "\n")
	if got := strings.Join(notes, "\n"); got != wantNotes {
		t.Fatalf("notes:\n%s", got)
	}
}

func TestCollectChallengeFallsBackToNames(t *testing.T) {
	resp := responses(t)
	resp["/v10/projects/prj_agent/env?teamId=team_acme"] = resp["challenge"]
	setup(t, resp)
	found, notes, err := Vercel{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := summary(found)
	if !strings.Contains(got, "support-agent · production | CLAUDE_API_KEY | anthropic | name-only") ||
		!strings.Contains(got, "support-agent · custom environment | MODEL_KEY | openai | 3e0b") {
		t.Fatalf("got:\n%s", got)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "Vercel didn't decrypt 1 AI-looking variable, so it was matched by name only.") {
		t.Fatalf("notes:\n%s", strings.Join(notes, "\n"))
	}
}

func TestCollectEverythingFails(t *testing.T) {
	resp := responses(t)
	broken := resp["/v10/projects/prj_broken/env?teamId=team_acme"]
	for _, k := range []string{"/v10/projects/prj_landing/env", "/v10/projects/prj_blog/env", "/v10/projects/prj_agent/env?teamId=team_acme"} {
		resp[k] = broken
	}
	setup(t, resp)
	if found, _, err := (Vercel{}).Collect(context.Background()); err == nil || len(found) != 0 {
		t.Fatalf("want an error when nothing could be read, got %v (%d found)", err, len(found))
	}
}

func TestCollectBadLogin(t *testing.T) {
	setup(t, responses(t))
	authPath = func() string {
		p := filepath.Join(t.TempDir(), "auth.json")
		os.WriteFile(p, []byte(`{"token":"expired"}`), 0o600)
		return p
	}
	_, _, err := Vercel{}.Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "vercel login") {
		t.Fatalf("want a login hint, got %v", err)
	}
}

func TestLogsPathOnly(t *testing.T) {
	setup(t, responses(t))
	var lines []string
	old := Logf
	Logf = func(format string, a ...any) { lines = append(lines, fmt.Sprintf(format, a...)) }
	t.Cleanup(func() { Logf = old })
	if _, _, err := (Vercel{}).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 8 {
		t.Fatalf("logged %d lines: %q", len(lines), lines)
	}
	for _, l := range lines {
		if strings.ContainsAny(l, "?&") || strings.Contains(l, testToken) {
			t.Fatalf("log line leaks more than method/host/path: %q", l)
		}
	}
}
