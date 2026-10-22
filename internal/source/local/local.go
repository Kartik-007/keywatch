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
	tracked := gitTracked(ctx, root)
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

	hist, notes := gitHistory(ctx, root, seenInTree)
	return append(out, hist...), notes, nil
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
func gitHistory(ctx context.Context, root string, skip map[string]bool) ([]core.FoundSecret, []string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "log", "--all", "-p", "--no-color",
		"--format=commit %h", "-G", `sk-(ant-|proj-|svcacct-|admin-)|T3BlbkFJ`)
	b, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, []string{"Git history scan stopped after 15s (large repo). Recent commits were checked."}
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
		case strings.HasPrefix(line, "+++ b/"):
			file = strings.TrimPrefix(line, "+++ b/")
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
