package railway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/source"
)

// Endpoint is Railway's public GraphQL API. Tests point it at httptest.
var Endpoint = "https://backboard.railway.com/graphql/v2"

// Logf logs each network call (method, host, path). The CLI wires it to
// --verbose; it never receives headers or bodies.
var Logf = func(format string, a ...any) {}

// configPath is where the Railway CLI keeps its login. Overridden in tests.
var configPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".railway", "config.json")
}

var client = &http.Client{Timeout: 30 * time.Second}

func init() { source.Register(Railway{}) }

// Railway is the Railway source.
type Railway struct{}

func (Railway) Name() string { return "railway" }

func (Railway) Available() bool { return token() != "" }

// token follows the Railway CLI's own order: RAILWAY_API_TOKEN, then the
// OAuth access token, then the legacy token from ~/.railway/config.json.
func token() string {
	if t := os.Getenv("RAILWAY_API_TOKEN"); t != "" {
		return t
	}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return ""
	}
	var cfg struct {
		User struct {
			Token       string `json:"token"`
			AccessToken string `json:"accessToken"`
		} `json:"user"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	if cfg.User.AccessToken != "" {
		return cfg.User.AccessToken
	}
	return cfg.User.Token
}

type edges[T any] struct {
	Edges []struct {
		Node T `json:"node"`
	} `json:"edges"`
}

type project struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	DeletedAt    *string `json:"deletedAt"`
	Environments edges[struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		CanAccess        *bool  `json:"canAccess"`
		ServiceInstances edges[struct {
			ServiceID string `json:"serviceId"`
		}] `json:"serviceInstances"`
	}] `json:"environments"`
	Services edges[struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}] `json:"services"`
}

// projectFields is shared by personal/team workspaces and external ones.
const projectFields = `id name deletedAt
  environments { edges { node { id name canAccess serviceInstances { edges { node { serviceId } } } } } }
  services { edges { node { id name } } }`

const projectsQuery = `query keywatchProjects {
  me { workspaces { id name projects(first: 500) { edges { node { ` + projectFields + ` } } } } }
  externalWorkspaces { id name projects { ` + projectFields + ` } }
}`

const variablesQuery = `query keywatchVariables($projectId: String!, $environmentId: String!, $serviceId: String) {
  variables(projectId: $projectId, environmentId: $environmentId, serviceId: $serviceId)
}`

// read is one variables call: a service in an environment, or the
// environment's shared variables when serviceID is empty.
type read struct {
	projectID, environmentID, serviceID string
	where                               string
}

func (Railway) Collect(ctx context.Context) ([]core.FoundSecret, []string, error) {
	tok := token()
	if tok == "" {
		return nil, nil, errors.New("railway: no Railway CLI login found (run `railway login`)")
	}
	projects, err := listProjects(ctx, tok)
	if err != nil {
		return nil, nil, fmt.Errorf("railway: listing projects: %w", err)
	}

	var reads []read
	for _, p := range projects {
		prefix := ""
		if len(projects) > 1 {
			prefix = p.Name + " / "
		}
		names := map[string]string{}
		for _, s := range p.Services.Edges {
			names[s.Node.ID] = s.Node.Name
		}
		for _, e := range p.Environments.Edges {
			env := e.Node
			if env.CanAccess != nil && !*env.CanAccess {
				continue
			}
			reads = append(reads, read{p.ID, env.ID, "", prefix + "shared · " + env.Name})
			for _, si := range env.ServiceInstances.Edges {
				id := si.Node.ServiceID
				name := names[id]
				if name == "" {
					name = id
				}
				reads = append(reads, read{p.ID, env.ID, id, prefix + name + " · " + env.Name})
			}
		}
	}

	var (
		found    []core.FoundSecret
		nameOnly int
		failed   []string
		errs     []error
	)
	for _, r := range reads {
		vars, err := variables(ctx, tok, r)
		if err != nil {
			failed = append(failed, r.where)
			errs = append(errs, fmt.Errorf("%s: %w", r.where, err))
			continue
		}
		for _, name := range sortedKeys(vars) {
			v := vars[name]
			loc := core.Location{Source: "railway", Where: r.where, Var: name}
			if v == "" {
				// Sealed variables are never returned by the API.
				if p := guessProvider(name); p != "" {
					loc.NameOnly = true
					found = append(found, core.FoundSecret{Provider: p, Location: loc})
					nameOnly++
				}
				continue
			}
			for _, m := range core.Detect(v) {
				found = append(found, core.FoundSecret{Provider: m.Provider, Value: m.Value, Location: loc})
			}
		}
	}

	if len(reads) > 0 && len(failed) == len(reads) {
		return nil, nil, fmt.Errorf("railway: couldn't read any variables: %w", errors.Join(errs...))
	}
	var notes []string
	if nameOnly > 0 {
		notes = append(notes, fmt.Sprintf("Railway sealed variables can't be read back, so %d %s matched by name only.", nameOnly, wasWere(nameOnly)))
	}
	if len(failed) > 0 {
		notes = append(notes, fmt.Sprintf("Skipped %d of %d Railway variable sets that couldn't be read: %s.", len(failed), len(reads), list(failed)))
	}
	return found, notes, nil
}

func listProjects(ctx context.Context, tok string) ([]project, error) {
	var data struct {
		Me struct {
			Workspaces []struct {
				Projects edges[project] `json:"projects"`
			} `json:"workspaces"`
		} `json:"me"`
		ExternalWorkspaces []struct {
			Projects []project `json:"projects"`
		} `json:"externalWorkspaces"`
	}
	if err := gql(ctx, tok, projectsQuery, nil, &data); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []project
	add := func(p project) {
		if p.DeletedAt != nil || seen[p.ID] {
			return
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	for _, w := range data.Me.Workspaces {
		for _, e := range w.Projects.Edges {
			add(e.Node)
		}
	}
	for _, w := range data.ExternalWorkspaces {
		for _, p := range w.Projects {
			add(p)
		}
	}
	return out, nil
}

func variables(ctx context.Context, tok string, r read) (map[string]string, error) {
	vars := map[string]any{"projectId": r.projectID, "environmentId": r.environmentID}
	if r.serviceID != "" {
		vars["serviceId"] = r.serviceID
	}
	var data struct {
		Variables map[string]any `json:"variables"`
	}
	if err := gql(ctx, tok, variablesQuery, vars, &data); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(data.Variables))
	for k, v := range data.Variables {
		s, _ := v.(string)
		out[k] = s
	}
	return out, nil
}

// gql posts one GraphQL query. Only method, host and path are logged.
func gql(ctx context.Context, tok, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
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
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	jsonErr := json.Unmarshal(b, &env)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("Railway rejected the saved login (HTTP %d); run `railway login`", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d%s", resp.StatusCode, firstMessage(env.Errors))
	}
	if jsonErr != nil {
		return fmt.Errorf("unexpected response: %w", jsonErr)
	}
	if len(env.Errors) > 0 {
		return errors.New(strings.TrimPrefix(firstMessage(env.Errors), ": "))
	}
	return json.Unmarshal(env.Data, out)
}

func firstMessage(errs []struct {
	Message string `json:"message"`
}) string {
	if len(errs) == 0 {
		return ""
	}
	return ": " + errs[0].Message
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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func wasWere(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// list shows up to three labels and a count of the rest.
func list(labels []string) string {
	if len(labels) <= 3 {
		return strings.Join(labels, ", ")
	}
	return strings.Join(labels[:3], ", ") + fmt.Sprintf(" and %d more", len(labels)-3)
}
