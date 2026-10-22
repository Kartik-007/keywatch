package vercel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/source"
)

// BaseURL is Vercel's REST API. Tests point it at httptest.
var BaseURL = "https://api.vercel.com"

// Logf logs each network call (method, host, path). The CLI wires it to
// --verbose; it never receives headers, query strings or bodies.
var Logf = func(format string, a ...any) {}

// authPath finds the Vercel CLI's auth.json. Overridden in tests.
var authPath = defaultAuthPath

var client = &http.Client{Timeout: 30 * time.Second}

func init() { source.Register(Vercel{}) }

// Vercel is the Vercel source.
type Vercel struct{}

func (Vercel) Name() string { return "vercel" }

func (Vercel) Available() bool { return token() != "" }

// defaultAuthPath mirrors the Vercel CLI: the first existing directory of
// the platform data dir for com.vercel.cli, the legacy ~/.now, and the
// legacy "now" data dir.
func defaultAuthPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dataDirs := func(app string) []string {
		var dirs []string
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			dirs = append(dirs, filepath.Join(x, app))
		}
		if runtime.GOOS == "darwin" {
			dirs = append(dirs, filepath.Join(home, "Library", "Application Support", app))
		} else {
			dirs = append(dirs, filepath.Join(home, ".local", "share", app))
		}
		return dirs
	}
	candidates := append(dataDirs("com.vercel.cli"), filepath.Join(home, ".now"))
	candidates = append(candidates, dataDirs("now")...)
	for _, d := range candidates {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return filepath.Join(d, "auth.json")
		}
	}
	return filepath.Join(candidates[0], "auth.json")
}

// token reads the CLI's saved access token. Logins stored in the OS keyring
// (credStorage "keyring", opt-in) aren't read in v0.1.
func token() string {
	b, err := os.ReadFile(authPath())
	if err != nil {
		return ""
	}
	var a struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(b, &a) != nil {
		return ""
	}
	return a.Token
}

type project struct {
	ID, Name string
	teamID   string
}

type envVar struct {
	Key                  string          `json:"key"`
	Value                string          `json:"value"`
	Type                 string          `json:"type"`
	Decrypted            bool            `json:"decrypted"`
	Target               json.RawMessage `json:"target"`
	CustomEnvironmentIDs []string        `json:"customEnvironmentIds"`
}

// apiError is Vercel's {"error":{"code","message"}} body plus the status.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	if e.Status == http.StatusUnauthorized {
		return "Vercel rejected the saved login (HTTP 401); run `vercel login`"
	}
	msg := fmt.Sprintf("HTTP %d", e.Status)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// challenged reports whether Vercel wants a fresh sign-in (step-up auth)
// before it decrypts values. The CLI handles these interactively; we can't.
func challenged(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Code {
	case "challenge_required", "challenge_required_email_otp", "user_auth_required":
		return true
	}
	return false
}

func (Vercel) Collect(ctx context.Context) ([]core.FoundSecret, []string, error) {
	tok := token()
	if tok == "" {
		return nil, nil, errors.New("vercel: no Vercel CLI login found (run `vercel login`)")
	}
	projects, err := listProjects(ctx, tok)
	if err != nil {
		return nil, nil, fmt.Errorf("vercel: listing projects: %w", err)
	}

	var (
		found             []core.FoundSecret
		sensitive, hidden int
		failed            []string
		errs              []error
	)
	for _, p := range projects {
		envs, decrypted, err := projectEnv(ctx, tok, p)
		if err != nil {
			failed = append(failed, p.Name)
			errs = append(errs, fmt.Errorf("%s: %w", p.Name, err))
			continue
		}
		for _, e := range envs {
			if e.Type == "system" {
				continue
			}
			loc := core.Location{Source: "vercel", Where: p.Name + " · " + targets(e), Var: e.Key}
			if e.Type == "plain" || (decrypted && e.Decrypted) {
				for _, m := range core.Detect(e.Value) {
					found = append(found, core.FoundSecret{Provider: m.Provider, Value: m.Value, Location: loc})
				}
				continue
			}
			prov := guessProvider(e.Key)
			if prov == "" {
				continue
			}
			loc.NameOnly = true
			found = append(found, core.FoundSecret{Provider: prov, Location: loc})
			if e.Type == "sensitive" {
				sensitive++
			} else {
				hidden++
			}
		}
	}

	if len(projects) > 0 && len(failed) == len(projects) {
		return nil, nil, fmt.Errorf("vercel: couldn't read any project's variables: %w", errors.Join(errs...))
	}
	var notes []string
	if sensitive > 0 {
		notes = append(notes, fmt.Sprintf("Vercel sensitive variables can't be read back, so %d %s matched by name only.", sensitive, wasWere(sensitive)))
	}
	if hidden > 0 {
		notes = append(notes, fmt.Sprintf("Vercel didn't decrypt %d AI-looking %s, so %s matched by name only. Run `vercel login` and scan again to check the values.", hidden, plural(hidden, "variable", "variables"), plural(hidden, "it was", "they were")))
	}
	if len(failed) > 0 {
		notes = append(notes, fmt.Sprintf("Skipped %d of %d Vercel projects that couldn't be read: %s.", len(failed), len(projects), list(failed)))
	}
	return found, notes, nil
}

// listProjects lists projects in the personal scope and in every team the
// user belongs to. A scope that fails is skipped unless every scope fails.
func listProjects(ctx context.Context, tok string) ([]project, error) {
	teams, teamErr := listTeams(ctx, tok)
	scopes := append([]string{""}, teams...)
	seen := map[string]bool{}
	var out []project
	var errs []error
	ok := 0
	for _, teamID := range scopes {
		ps, err := scopeProjects(ctx, tok, teamID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ok++
		for _, p := range ps {
			if !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, p)
			}
		}
	}
	if ok == 0 {
		if teamErr != nil {
			errs = append(errs, teamErr)
		}
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func listTeams(ctx context.Context, tok string) ([]string, error) {
	var ids []string
	until := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"limit": {"100"}}
		if until != "" {
			q.Set("until", until)
		}
		var resp struct {
			Teams []struct {
				ID string `json:"id"`
			} `json:"teams"`
			Pagination struct {
				Next *int64 `json:"next"`
			} `json:"pagination"`
		}
		if err := get(ctx, tok, "/v2/teams", q, &resp); err != nil {
			return ids, err
		}
		for _, t := range resp.Teams {
			ids = append(ids, t.ID)
		}
		if resp.Pagination.Next == nil || len(resp.Teams) == 0 {
			break
		}
		until = strconv.FormatInt(*resp.Pagination.Next, 10)
	}
	return ids, nil
}

func scopeProjects(ctx context.Context, tok, teamID string) ([]project, error) {
	var out []project
	until := ""
	for page := 0; page < 50; page++ {
		q := url.Values{"limit": {"100"}}
		if teamID != "" {
			q.Set("teamId", teamID)
		}
		if until != "" {
			q.Set("until", until)
		}
		var resp struct {
			Projects []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"projects"`
			Pagination struct {
				Next *int64 `json:"next"`
			} `json:"pagination"`
		}
		if err := get(ctx, tok, "/v10/projects", q, &resp); err != nil {
			return nil, err
		}
		for _, p := range resp.Projects {
			out = append(out, project{ID: p.ID, Name: p.Name, teamID: teamID})
		}
		if resp.Pagination.Next == nil || len(resp.Projects) == 0 {
			break
		}
		until = strconv.FormatInt(*resp.Pagination.Next, 10)
	}
	return out, nil
}

// projectEnv reads a project's env vars, decrypted where Vercel allows. If
// Vercel asks for a fresh sign-in first, it falls back to names only and
// reports decrypted=false.
func projectEnv(ctx context.Context, tok string, p project) (envs []envVar, decrypted bool, err error) {
	q := url.Values{"decrypt": {"true"}}
	if p.teamID != "" {
		q.Set("teamId", p.teamID)
	}
	var resp struct {
		Envs []envVar `json:"envs"`
	}
	path := "/v10/projects/" + url.PathEscape(p.ID) + "/env"
	err = get(ctx, tok, path, q, &resp)
	if challenged(err) {
		q.Del("decrypt")
		resp.Envs = nil
		err = get(ctx, tok, path, q, &resp)
		return resp.Envs, false, err
	}
	return resp.Envs, true, err
}

// targets labels where a variable applies, e.g. "production" or
// "production, preview".
func targets(e envVar) string {
	var ts []string
	if json.Unmarshal(e.Target, &ts) != nil {
		var one string
		if json.Unmarshal(e.Target, &one) == nil && one != "" {
			ts = []string{one}
		}
	}
	if len(ts) == 0 && len(e.CustomEnvironmentIDs) > 0 {
		return "custom environment"
	}
	if len(ts) == 0 {
		return "all environments"
	}
	return strings.Join(ts, ", ")
}

// get fetches one JSON resource. Only method, host and path are logged.
func get(ctx context.Context, tok, path string, q url.Values, out any) error {
	u := BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "keywatch")
	Logf("%s %s %s", req.Method, req.URL.Host, req.URL.Path)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(b, &body)
		return &apiError{Status: resp.StatusCode, Code: body.Error.Code, Message: body.Error.Message}
	}
	return json.Unmarshal(b, out)
}

// guessProvider names the provider a variable probably holds a key for,
// judging by its name alone. Empty means it doesn't look AI-related.
func guessProvider(name string) string {
	n := strings.ToUpper(name)
	switch {
	case strings.Contains(n, "ANTHROPIC"), strings.Contains(n, "CLAUDE"):
		return core.Anthropic
	case strings.Contains(n, "OPENAI"):
		return core.OpenAI
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func wasWere(n int) string { return plural(n, "was", "were") }

// list shows up to three labels and a count of the rest.
func list(labels []string) string {
	if len(labels) <= 3 {
		return strings.Join(labels, ", ")
	}
	return strings.Join(labels[:3], ", ") + fmt.Sprintf(" and %d more", len(labels)-3)
}
