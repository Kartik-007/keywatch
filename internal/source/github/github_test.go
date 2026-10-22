package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Kartik-007/keywatch/internal/core"
)

const testToken = "gh-test-token"

func responses(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "github", "responses.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// fakeCmds stubs local commands: keys are "name arg arg…".
func fakeCmds(t *testing.T, cmds map[string]string) {
	t.Helper()
	oldRun, oldLook := run, lookPath
	run = func(_ context.Context, name string, args ...string) (string, error) {
		out, ok := cmds[strings.Join(append([]string{name}, args...), " ")]
		if !ok {
			return "", errors.New("exit status 1")
		}
		return out, nil
	}
	lookPath = func(file string) (string, error) {
		if _, ok := cmds[file]; ok {
			return "/usr/local/bin/" + file, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { run, lookPath = oldRun, oldLook })
}

func loggedIn(t *testing.T) {
	fakeCmds(t, map[string]string{
		"gh":                                  "",
		"git remote get-url origin":           "git@github.com:acme/support-bot.git",
		"gh auth token --hostname github.com": testToken,
	})
}

// serve answers GitHub API calls from recorded responses keyed by escaped
// path. A body with a "status" field is returned with that status code.
func serve(t *testing.T, resp map[string]json.RawMessage) {
	t.Helper()
	old := BaseURL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-GitHub-Api-Version") == "" || r.URL.Query().Get("per_page") != "100" {
			t.Errorf("missing API version or page size on %s", r.URL)
		}
		body, ok := resp[r.URL.EscapedPath()]
		if !ok {
			t.Errorf("unexpected request %s", r.URL.EscapedPath())
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
	t.Cleanup(func() { srv.Close(); BaseURL = old })
}

func summary(found []core.FoundSecret) string {
	var out []string
	for _, f := range found {
		out = append(out, fmt.Sprintf("%s | %s | %s | %s | nameOnly=%v value=%q",
			f.Location.Source, f.Location.Where, f.Location.Var, f.Provider, f.Location.NameOnly, f.Value))
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestParseRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/support-bot.git":         "acme/support-bot",
		"https://github.com/acme/support-bot":         "acme/support-bot",
		"https://github.com/acme/support.bot.git":     "acme/support.bot",
		"ssh://git@github.com/acme/support-bot.git/":  "acme/support-bot",
		"https://user@github.com/Acme-Inc/api_v2.git": "Acme-Inc/api_v2",
	} {
		if got, err := parseRemote(in); err != nil || got != want {
			t.Errorf("%s: got %q, %v", in, got, err)
		}
	}
	if _, err := parseRemote("git@gitlab.com:acme/x.git"); err == nil {
		t.Error("gitlab remote accepted")
	}
}

func TestAvailable(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	dir := t.TempDir()
	old := hostsPath
	hostsPath = func() string { return filepath.Join(dir, "hosts.yml") }
	t.Cleanup(func() { hostsPath = old })
	loggedIn(t)

	if (GitHub{}).Available() {
		t.Fatal("available without a gh login")
	}
	os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte("github.com:\n    users:\n        kartik:\n    git_protocol: https\n    user: kartik\n"), 0o600)
	if !(GitHub{}).Available() {
		t.Fatal("not available with a gh login")
	}
	fakeCmds(t, map[string]string{"gh": ""}) // no git origin
	if (GitHub{}).Available() {
		t.Fatal("available outside a GitHub repo")
	}
}

func TestCollect(t *testing.T) {
	loggedIn(t)
	serve(t, responses(t))
	found, notes, err := GitHub{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		`github | GitHub env production · OPENAI_API_KEY | OPENAI_API_KEY | openai | nameOnly=true value=""`,
		`github | GitHub env staging/eu · claude_api_key | claude_api_key | anthropic | nameOnly=true value=""`,
		`github | GitHub secret · ANTHROPIC_API_KEY | ANTHROPIC_API_KEY | anthropic | nameOnly=true value=""`,
		`github | GitHub secret · OPENAI_API_KEY | OPENAI_API_KEY | openai | nameOnly=true value=""`,
	}, "\n")
	if got := summary(found); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	wantNotes := strings.Join([]string{
		"GitHub secrets can't be read back, so 4 were matched by name only.",
		"Skipped GitHub secrets that couldn't be listed: env broken.",
	}, "\n")
	if got := strings.Join(notes, "\n"); got != wantNotes {
		t.Fatalf("notes:\n%s", got)
	}
}

func TestCollectNoAINames(t *testing.T) {
	loggedIn(t)
	serve(t, map[string]json.RawMessage{
		"/repos/acme/support-bot/actions/secrets": json.RawMessage(`{"total_count":1,"secrets":[{"name":"DEPLOY_TOKEN"}]}`),
		"/repos/acme/support-bot/environments":    json.RawMessage(`{"total_count":0,"environments":[]}`),
	})
	found, notes, err := GitHub{}.Collect(context.Background())
	if err != nil || len(found) != 0 {
		t.Fatalf("got %d found, err %v", len(found), err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "none of the 1 in acme/support-bot") {
		t.Fatalf("notes: %q", notes)
	}
}

func TestCollectNoAccess(t *testing.T) {
	loggedIn(t)
	serve(t, map[string]json.RawMessage{
		"/repos/acme/support-bot/actions/secrets": json.RawMessage(`{"status":403,"message":"Resource not accessible by integration"}`),
		"/repos/acme/support-bot/environments":    json.RawMessage(`{"status":404,"message":"Not Found"}`),
	})
	_, _, err := GitHub{}.Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "acme/support-bot") {
		t.Fatalf("want an error naming the repo, got %v", err)
	}
}

func TestLogsRequests(t *testing.T) {
	loggedIn(t)
	serve(t, responses(t))
	var lines []string
	old := Logf
	Logf = func(format string, a ...any) { lines = append(lines, fmt.Sprintf(format, a...)) }
	t.Cleanup(func() { Logf = old })
	GitHub{}.Collect(context.Background())
	if len(lines) != 5 || !strings.HasSuffix(lines[0], " /repos/acme/support-bot/actions/secrets") {
		t.Fatalf("logged %q", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "?") || strings.Contains(l, testToken) {
			t.Fatalf("log line leaks more than method/host/path: %q", l)
		}
	}
}
