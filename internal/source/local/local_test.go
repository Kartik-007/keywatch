package local_test

import (
	"context"
	"os/exec"
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
