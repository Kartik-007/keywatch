package local_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/source/local"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCollectFixture(t *testing.T) {
	dir := testutil.FixtureProject(t)
	git(t, dir, "init", "-q")
	git(t, dir, "add", ".env.local")
	git(t, dir, "commit", "-qm", "oops")
	local.Dir = dir

	got, _, err := local.Local{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	keys := core.Join(got, nil)
	if len(keys) != 2 {
		t.Fatalf("want 2 distinct keys, got %d: %+v", len(keys), keys)
	}
	var sawCommittedEnv, sawUncommittedTS bool
	for _, s := range got {
		if s.Location.Where == ".env.local" && s.Location.Committed && s.Location.Var == "OPENAI_API_KEY" {
			sawCommittedEnv = true
		}
		if s.Location.Where == "src/agent.ts" && !s.Location.Committed {
			sawUncommittedTS = true
		}
	}
	if !sawCommittedEnv || !sawUncommittedTS {
		t.Fatalf("locations wrong: %+v", got)
	}
}

func TestCollectHistory(t *testing.T) {
	dir := testutil.FixtureProject(t)
	git(t, dir, "init", "-q")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "add keys")
	git(t, dir, "rm", "-q", "src/agent.ts")
	git(t, dir, "commit", "-qm", "remove")
	local.Dir = dir

	got, _, _ := local.Local{}.Collect(context.Background())
	found := false
	for _, s := range got {
		if s.Value == testutil.Anthropic1 && s.Location.Committed {
			found = true
		}
	}
	if !found {
		t.Fatal("deleted-but-committed key not found in history")
	}
}

// A plain folder holding several repos (like ~/code/myproduct/{backend,frontend})
// must still catch keys committed inside each repo.
func TestCollectNestedRepos(t *testing.T) {
	parent := t.TempDir()
	backend := filepath.Join(parent, "backend")
	os.MkdirAll(backend, 0o755)
	os.WriteFile(filepath.Join(backend, ".env"), []byte("OPENAI_API_KEY="+testutil.OpenAI1+"\n"), 0o644)
	os.WriteFile(filepath.Join(backend, "old.ts"), []byte(`const k = "`+testutil.Anthropic1+`";`), 0o644)
	git(t, backend, "init", "-q")
	git(t, backend, "add", ".")
	git(t, backend, "commit", "-qm", "oops")
	git(t, backend, "rm", "-q", "old.ts")
	git(t, backend, "commit", "-qm", "remove")
	local.Dir = parent

	got, _, err := local.Local{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var envCommitted, historyFound bool
	for _, s := range got {
		if s.Location.Where == filepath.Join("backend", ".env") && s.Location.Committed {
			envCommitted = true
		}
		if s.Value == testutil.Anthropic1 && s.Location.Committed && strings.Contains(s.Location.Where, filepath.Join("backend", "old.ts")) {
			historyFound = true
		}
	}
	if !envCommitted || !historyFound {
		t.Fatalf("nested repo not checked (env committed=%v, history=%v): %+v", envCommitted, historyFound, got)
	}
}
