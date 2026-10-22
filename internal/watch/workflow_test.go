package watch

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

var golden = filepath.Join("..", "..", "testdata", "watch", "keywatch.yml")

func TestWorkflowGolden(t *testing.T) {
	got := Workflow(50, []string{"anthropic", "openai"}, true)
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("workflow differs from %s (run go test ./internal/watch -update):\n%s", golden, got)
	}
}

func TestWorkflowIsValidYAML(t *testing.T) {
	doc, err := parseYAML(Workflow(50, []string{"openai", "anthropic"}, true))
	if err != nil {
		t.Fatal(err)
	}
	get := func(path ...any) any {
		var v any = doc
		for _, p := range path {
			switch k := p.(type) {
			case string:
				m, ok := v.(map[string]any)
				if !ok {
					t.Fatalf("%v: not a mapping at %q", path, k)
				}
				v = m[k]
			case int:
				s, ok := v.([]any)
				if !ok || k >= len(s) {
					t.Fatalf("%v: no item %d", path, k)
				}
				v = s[k]
			}
		}
		return v
	}

	if c := get("on", "schedule", 0, "cron"); c != "17 * * * *" {
		t.Errorf("cron = %v", c)
	}
	if _, ok := get("on").(map[string]any)["workflow_dispatch"]; !ok {
		t.Error("missing workflow_dispatch")
	}
	if p := get("permissions"); !reflect.DeepEqual(p, map[string]any{"contents": "read", "actions": "read"}) {
		t.Errorf("permissions = %v", p)
	}
	if v := get("jobs", "watch", "env", "KEYWATCH_VERSION"); v != ReleaseVersion {
		t.Errorf("version = %v", v)
	}
	install := get("jobs", "watch", "steps", 1, "run").(string)
	for _, s := range []string{"github.com/Kartik-007/keywatch/releases/download/", "sha256sum --check"} {
		if !strings.Contains(install, s) {
			t.Errorf("install step missing %q", s)
		}
	}
	if r := get("jobs", "watch", "steps", 2, "run"); r != "keywatch watch run --scary-day 50" {
		t.Errorf("run = %v", r)
	}
	env := get("jobs", "watch", "steps", 2, "env")
	want := map[string]any{
		"KEYWATCH_OPENAI_ADMIN_KEY":    "${{ secrets.KEYWATCH_OPENAI_ADMIN_KEY }}",
		"KEYWATCH_ANTHROPIC_ADMIN_KEY": "${{ secrets.KEYWATCH_ANTHROPIC_ADMIN_KEY }}",
		"KEYWATCH_WEBHOOK":             "${{ secrets.KEYWATCH_WEBHOOK }}",
		"KEYWATCH_VARS":                "${{ toJSON(vars) }}",
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %v", env)
	}
}

func TestWorkflowOnlyNamesConnectedSecrets(t *testing.T) {
	w := Workflow(12.5, []string{"anthropic"}, false)
	if _, err := parseYAML(w); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w, "OPENAI") || strings.Contains(w, "KEYWATCH_WEBHOOK") {
		t.Errorf("unexpected secrets in:\n%s", w)
	}
	if !strings.Contains(w, "--scary-day 12.5") || !strings.Contains(w, "secrets.KEYWATCH_ANTHROPIC_ADMIN_KEY") {
		t.Errorf("missing scary day or anthropic secret:\n%s", w)
	}
}

func TestParseYAMLRejectsBrokenFiles(t *testing.T) {
	for _, bad := range []string{
		"a:\n\tb: c",
		"a: b\n   c: d",
		"a: b\na: c",
		"cron: * * * * *",
		"a: b: c",
		`a: "unterminated`,
	} {
		if _, err := parseYAML(bad); err == nil {
			t.Errorf("want error for %q", bad)
		}
	}
}

func TestAckVar(t *testing.T) {
	for id, want := range map[string]string{
		"a91f": "KEYWATCH_ACK_A91F",
		"ig-A": "KEYWATCH_ACK_IG_A",
		"org":  "KEYWATCH_ACK_ORG",
	} {
		if got := AckVar(id); got != want {
			t.Errorf("AckVar(%q) = %q, want %q", id, got, want)
		}
	}
}
