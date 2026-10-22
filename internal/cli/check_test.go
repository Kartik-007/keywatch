package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/provider"
	"github.com/Kartik-007/keywatch/internal/source"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

type checkTestSource struct {
	name  string
	found []core.FoundSecret
	err   error
	read  *bool
}

func (s checkTestSource) Name() string    { return s.name }
func (s checkTestSource) Available() bool { return true }
func (s checkTestSource) Collect(context.Context) ([]core.FoundSecret, []string, error) {
	*s.read = true
	return s.found, nil, s.err
}

// checkSetup registers a local source with found, a platform source and a
// connected provider that must never be read.
func checkSetup(t *testing.T, found []core.FoundSecret, err error) (platformRead *bool, p *watchTestProvider) {
	t.Helper()
	var localRead, railwayRead bool
	savedS, savedP := source.All(), provider.All()
	source.Reset(
		checkTestSource{name: "local", found: found, err: err, read: &localRead},
		checkTestSource{name: "railway", read: &railwayRead, found: []core.FoundSecret{{Provider: core.OpenAI, Value: testutil.OpenAI2}}},
	)
	p = &watchTestProvider{}
	provider.Reset(p)
	t.Setenv("KEYWATCH_OPENAI_ADMIN_KEY", "test-admin")
	t.Cleanup(func() { source.Reset(savedS...); provider.Reset(savedP...); JSON = false })
	return &railwayRead, p
}

func TestCheckFailsOnCommittedKey(t *testing.T) {
	platformRead, p := checkSetup(t, []core.FoundSecret{
		{Provider: core.OpenAI, Value: testutil.OpenAI1, Location: core.Location{Source: "local", Where: ".env.local", Committed: true}},
		{Provider: core.OpenAI, Value: testutil.OpenAI1, Location: core.Location{Source: "local", Where: "src/agent.ts", Committed: true}},
		{Provider: core.Anthropic, Value: testutil.Anthropic1, Location: core.Location{Source: "local", Where: ".env"}},
	}, nil)
	out, err := execWatchCmd(t, "check")
	if err == nil || !strings.Contains(err.Error(), "1 AI key(s) committed") {
		t.Fatalf("err = %v", err)
	}
	for _, s := range []string{"sk-…a91f (openai) is committed to git in .env.local", "in src/agent.ts"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q in:\n%s", s, out)
		}
	}
	if strings.Contains(out, "77c2") {
		t.Error("uncommitted key reported")
	}
	if strings.Contains(out, testutil.OpenAI1) {
		t.Fatal("full key printed")
	}
	if *platformRead || p.listed {
		t.Error("check must stay offline: read a platform or provider")
	}
	if got := provider.All(); len(got) != 1 || got[0] != provider.Provider(p) {
		t.Error("providers not restored after check")
	}
}

func TestCheckClean(t *testing.T) {
	checkSetup(t, []core.FoundSecret{
		{Provider: core.Anthropic, Value: testutil.Anthropic1, Location: core.Location{Source: "local", Where: ".env"}},
	}, nil)
	out, err := execWatchCmd(t, "check")
	if err != nil || !strings.Contains(out, "No AI keys committed") {
		t.Errorf("err=%v out:\n%s", err, out)
	}
}

func TestCheckJSON(t *testing.T) {
	checkSetup(t, []core.FoundSecret{
		{Provider: core.OpenAI, Value: testutil.OpenAI1, Location: core.Location{Source: "local", Where: ".env.local", Committed: true}},
	}, nil)
	out, err := execWatchCmd(t, "check", "--json")
	if err == nil {
		t.Fatal("want non-zero exit")
	}
	var got struct{ Committed []committedKey }
	if e := json.Unmarshal([]byte(out), &got); e != nil || len(got.Committed) != 1 || got.Committed[0].ID != "a91f" {
		t.Errorf("json %v: %s", e, out)
	}
}

func TestCheckScanErrorFails(t *testing.T) {
	checkSetup(t, nil, errors.New("git not found"))
	if _, err := execWatchCmd(t, "check"); err == nil || !strings.Contains(err.Error(), "git not found") {
		t.Errorf("err = %v", err)
	}
}
