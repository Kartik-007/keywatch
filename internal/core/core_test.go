package core_test

import (
	"testing"

	"github.com/Kartik-007/keywatch/internal/core"
	"github.com/Kartik-007/keywatch/internal/testutil"
)

func TestDetect(t *testing.T) {
	text := "a=" + testutil.OpenAI1 + "\nb=" + testutil.Anthropic1 + "\nc=" + testutil.OpenAI1
	got := core.Detect(text)
	if len(got) != 2 || got[0].Provider != core.Anthropic || got[1].Provider != core.OpenAI {
		t.Fatalf("got %+v", got)
	}
}

func TestRedactNeverLeaks(t *testing.T) {
	if r := core.Redact(testutil.OpenAI1); r != "sk-…a91f" {
		t.Fatal(r)
	}
	if r := core.Redact(testutil.Anthropic1); r != "sk-ant-…77c2" {
		t.Fatal(r)
	}
}

func TestJoin(t *testing.T) {
	found := []core.FoundSecret{
		{Provider: core.OpenAI, Value: testutil.OpenAI1, Location: core.Location{Source: "local", Where: ".env.local", Committed: true}},
		{Provider: core.OpenAI, Value: testutil.OpenAI1, Location: core.Location{Source: "railway", Where: "support-bot · production"}},
	}
	remote := []core.ProviderKey{
		{Provider: core.OpenAI, ID: "key_1", Hint: testutil.OpenAI1Hint, Active: true},
		{Provider: core.OpenAI, ID: "key_2", Hint: "sk-" + "proj-zz...9999", Name: "old laptop", Active: true},
	}
	keys := core.Join(found, remote)
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d", len(keys))
	}
	if keys[0].Label != "support-bot · production" || keys[0].Remote == nil || len(keys[0].Locations) != 2 {
		t.Fatalf("bad joined key: %+v", keys[0])
	}
	if keys[1].Label != "old laptop" || len(keys[1].Locations) != 0 {
		t.Fatalf("bad orphan key: %+v", keys[1])
	}
}
