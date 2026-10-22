// Package local scans the current folder and its git history.
package local

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/source"
)

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	".next": true, ".venv": true, "venv": true, "__pycache__": true, "target": true,
}

const maxFileSize = 1 << 20

// Dir is the folder to scan. Tests override it.
var Dir = "."

type Local struct{}

func init() { source.Register(Local{}) }

func (Local) Name() string    { return "local" }
func (Local) Available() bool { return true }

func (Local) Collect(ctx context.Context) ([]core.FoundSecret, []string, error) {
	root, err := filepath.Abs(Dir)
	if err != nil {
		return nil, nil, err
	}
	// A folder can hold several repos (e.g. backend/ and frontend/ side by
	// side), so committed-ness and history are checked per repo.
	repos := findRepos(root)
	tracked := map[string]bool{}
	for _, repo := range repos {
		for f := range gitTracked(ctx, filepath.Join(root, repo)) {
			tracked[filepath.Join(repo, f)] = true
		}
	}
	var out []core.FoundSecret
	seenInTree := map[string]bool{}

	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileSize || !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(b, 0) >= 0 { // skip binaries
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for _, m := range core.Detect(string(b)) {
			seenInTree[m.Value] = true
			out = append(out, core.FoundSecret{
				Provider: m.Provider, Value: m.Value,
				Location: core.Location{Source: "local", Where: rel, Var: varName(string(b), m.Value), Committed: tracked[rel]},
			})
		}
		return nil
	})
	if err != nil {
		return out, nil, err
	}

	var notes []string
	for _, repo := range repos {
		hist, n := gitHistory(ctx, filepath.Join(root, repo), repo, seenInTree)
		out = append(out, hist...)
		notes = append(notes, n...)
	}
	return out, notes, nil
}

// findRepos returns git repos to check, as paths relative to root: "." for
// root itself (or the repo it sits inside), plus every repo nested below it.
func findRepos(root string) []string {
	repos := []string{"."}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == root {
			return nil
		}
		if skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			rel, _ := filepath.Rel(root, p)
			repos = append(repos, rel)
		}
		return nil
	})
	return repos
}

// gitTracked returns the set of files git tracks under root, or nil outside
// a repo.
func gitTracked(ctx context.Context, root string) map[string]bool {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z")
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, f := range strings.Split(string(b), "\x00") {
		if f != "" {
			set[filepath.FromSlash(f)] = true
		}
	}
	return set
}

// gitHistory finds keys that were committed at some point but are no longer
// in the working tree. Bounded to 15s so huge repos don't stall the scan.
func gitHistory(ctx context.Context, root, prefix string, skip map[string]bool) ([]core.FoundSecret, []string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "log", "--all", "-p", "--no-color",
		"--format=commit %h", "-G", `sk-(ant-|proj-|svcacct-|admin-)|T3BlbkFJ`)
	b, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, []string{fmt.Sprintf("Git history scan of %s stopped after 15s (large repo). Recent commits were checked.", repoName(prefix))}
	}
	if err != nil {
		return nil, nil
	}
	var out []core.FoundSecret
	seen := map[string]bool{}
	var commit, file string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "commit "):
			commit = strings.TrimPrefix(line, "commit ")
		case strings.HasPrefix(line, "diff --git a/"):
			// "diff --git a/x b/x" names the file even when it was deleted
			// (then the "+++" line is /dev/null).
			if i := strings.LastIndex(line, " b/"); i > 0 {
				file = filepath.Join(prefix, line[i+3:])
			}
		case strings.HasPrefix(line, "+++ b/") || strings.HasPrefix(line, "--- a/") ||
			line == "+++ /dev/null" || line == "--- /dev/null":
			continue
		case strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-"):
			for _, m := range core.Detect(line) {
				if skip[m.Value] || seen[m.Value] {
					continue
				}
				seen[m.Value] = true
				out = append(out, core.FoundSecret{
					Provider: m.Provider, Value: m.Value,
					Location: core.Location{Source: "local", Where: fmt.Sprintf("git history · %s (%s)", file, commit), Committed: true},
				})
			}
		}
	}
	return out, nil
}

func repoName(prefix string) string {
	if prefix == "." {
		return "this repo"
	}
	return prefix
}

// varName guesses the variable a secret is assigned to on its line, e.g.
// OPENAI_API_KEY=sk-... → "OPENAI_API_KEY".
func varName(text, secret string) string {
	i := strings.Index(text, secret)
	if i < 0 {
		return ""
	}
	start := strings.LastIndexByte(text[:i], '\n') + 1
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text[start:i]), "export "))
	for _, sep := range []string{"=", ":"} {
		if j := strings.Index(line, sep); j > 0 {
			name := strings.Trim(strings.TrimSpace(line[:j]), `"'`)
			if !strings.ContainsAny(name, " ({") {
				return name
			}
		}
	}
	return ""
}
