package github

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/source"
)

// BaseURL is GitHub's REST API. Tests point it at httptest.
var BaseURL = "https://api.github.com"

// Logf logs each network call (method, host, path). The CLI wires it to
// --verbose; it never receives headers, query strings or bodies.
var Logf = func(format string, a ...any) {}

// Hooks for tests: running local commands, finding gh, and gh's hosts file.
var (
	run       = runCmd
	lookPath  = exec.LookPath
	hostsPath = defaultHostsPath
)

const apiVersion = "2026-03-10"

var client = &http.Client{Timeout: 30 * time.Second}

func init() { source.Register(GitHub{}) }

// GitHub is the GitHub Actions secrets source.
type GitHub struct{}

func (GitHub) Name() string { return "github" }

// Available is true when gh is installed, logged in to github.com (judged
// from its hosts file or GH_TOKEN, not the network) and the current
// directory's origin is a GitHub repo.
func (GitHub) Available() bool {
	if _, err := lookPath("gh"); err != nil {
		return false
	}
	if os.Getenv("GH_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" && !hostsHasGitHub() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := currentRepo(ctx)
	return err == nil
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// defaultHostsPath follows gh's config dir order: GH_CONFIG_DIR,
// $XDG_CONFIG_HOME/gh, %AppData%/GitHub CLI on Windows, ~/.config/gh.
func defaultHostsPath() string {
	if d := os.Getenv("GH_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "hosts.yml")
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "gh", "hosts.yml")
	}
	if d := os.Getenv("AppData"); runtime.GOOS == "windows" && d != "" {
		return filepath.Join(d, "GitHub CLI", "hosts.yml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gh", "hosts.yml")
}

// hostsHasGitHub reports whether gh's hosts.yml has a github.com entry.
// The token itself may live in the OS keyring, so we don't look for it.
func hostsHasGitHub() bool {
	f, err := os.Open(hostsPath())
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimRight(sc.Text(), " \t") == "github.com:" {
			return true
		}
	}
	return false
}

var remoteRE = regexp.MustCompile(`github\.com[:/]+([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// currentRepo returns "owner/repo" for the current directory's origin.
func currentRepo(ctx context.Context) (string, error) {
	out, err := run(ctx, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", errors.New("the current folder has no git origin")
	}
	return parseRemote(out)
}

func parseRemote(remote string) (string, error) {
	m := remoteRE.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return "", errors.New("origin is not a github.com repository")
	}
	return m[1] + "/" + m[2], nil
}

func (GitHub) Collect(ctx context.Context) ([]core.FoundSecret, []string, error) {
	repo, err := currentRepo(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("github: %w", err)
	}
	tok, err := run(ctx, "gh", "auth", "token", "--hostname", "github.com")
	if err != nil || tok == "" {
		return nil, nil, errors.New("github: no gh login found (run `gh auth login`)")
	}

	var (
		found  []core.FoundSecret
		total  int
		failed []string
		errs   []error
		listed int // secret lists read successfully
	)
	add := func(where string, names []string) {
		listed++
		for _, n := range names {
			total++
			if p := guessProvider(n); p != "" {
				found = append(found, core.FoundSecret{Provider: p, Location: core.Location{
					Source: "github", Where: where + " · " + n, Var: n, NameOnly: true,
				}})
			}
		}
	}

	names, err := secretNames(ctx, tok, "/repos/"+repo+"/actions/secrets")
	if err != nil {
		failed = append(failed, "repository secrets")
		errs = append(errs, fmt.Errorf("repository secrets: %w", err))
	} else {
		add("GitHub secret", names)
	}

	envs, err := environments(ctx, tok, repo)
	if err != nil {
		failed = append(failed, "environments")
		errs = append(errs, fmt.Errorf("environments: %w", err))
	}
	for _, env := range envs {
		names, err := secretNames(ctx, tok, "/repos/"+repo+"/environments/"+url.PathEscape(env)+"/secrets")
		if err != nil {
			failed = append(failed, "env "+env)
			errs = append(errs, fmt.Errorf("env %s: %w", env, err))
			continue
		}
		add("GitHub env "+env, names)
	}

	if listed == 0 {
		return nil, nil, fmt.Errorf("github: couldn't list secrets for %s: %w", repo, errors.Join(errs...))
	}
	var notes []string
	switch n := len(found); {
	case n > 0:
		notes = append(notes, fmt.Sprintf("GitHub secrets can't be read back, so %d %s matched by name only.", n, plural(n, "was", "were")))
	case total > 0:
		notes = append(notes, fmt.Sprintf("GitHub secrets can't be read back, and none of the %d in %s look AI-related by name.", total, repo))
	}
	if len(failed) > 0 {
		notes = append(notes, fmt.Sprintf("Skipped GitHub secrets that couldn't be listed: %s.", strings.Join(failed, ", ")))
	}
	return found, notes, nil
}

// secretNames lists every secret name at a secrets endpoint, 100 per page.
func secretNames(ctx context.Context, tok, path string) ([]string, error) {
	var names []string
	for page := 1; page <= 50; page++ {
		var resp struct {
			TotalCount int `json:"total_count"`
			Secrets    []struct {
				Name string `json:"name"`
			} `json:"secrets"`
		}
		if err := get(ctx, tok, path, page, &resp); err != nil {
			return nil, err
		}
		for _, s := range resp.Secrets {
			names = append(names, s.Name)
		}
		if len(resp.Secrets) < 100 || len(names) >= resp.TotalCount {
			break
		}
	}
	return names, nil
}

func environments(ctx context.Context, tok, repo string) ([]string, error) {
	var names []string
	for page := 1; page <= 50; page++ {
		var resp struct {
			TotalCount   int `json:"total_count"`
			Environments []struct {
				Name string `json:"name"`
			} `json:"environments"`
		}
		if err := get(ctx, tok, "/repos/"+repo+"/environments", page, &resp); err != nil {
			return nil, err
		}
		for _, e := range resp.Environments {
			names = append(names, e.Name)
		}
		if len(resp.Environments) < 100 || len(names) >= resp.TotalCount {
			break
		}
	}
	return names, nil
}

// get fetches one page of a JSON list. Only method, host and path are logged.
func get(ctx context.Context, tok, path string, page int, out any) error {
	u := BaseURL + path + "?per_page=100&page=" + strconv.Itoa(page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "keywatch")
	Logf("%s %s %s", req.Method, req.URL.Host, req.URL.EscapedPath())
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return json.Unmarshal(b, out)
	case http.StatusUnauthorized:
		return errors.New("GitHub rejected the gh login (HTTP 401); run `gh auth login`")
	case http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("HTTP %d: your gh login can't see this repo's secrets (needs collaborator access and the repo scope)", resp.StatusCode)
	}
	var body struct {
		Message string `json:"message"`
	}
	json.Unmarshal(b, &body)
	if body.Message != "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body.Message)
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

// guessProvider names the provider a secret probably holds a key for,
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
