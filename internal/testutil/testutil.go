// Package testutil builds obviously fake keys at runtime. Keys are assembled
// from parts so no key-shaped string is committed to the repo (GitHub push
// protection and every secret scanner would rightly flag it).
package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const pad = "FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE"

// Fake keys. The last 4 characters are the ids tests assert on.
var (
	OpenAI1    = "sk-" + "proj-" + "keywatchDEMO" + pad + "a91f"
	OpenAI2    = "sk-" + "proj-" + "keywatchDEMO" + pad + "3e0b"
	Anthropic1 = "sk-" + "ant-" + "api03-" + "keywatchDEMO" + pad + "77c2"
	// OpenAIHint and AnthropicHint are what the providers' admin APIs would
	// return as redacted_value / partial_key_hint for the keys above.
	OpenAI1Hint    = "sk-" + "proj-key...a91f"
	Anthropic1Hint = "sk-" + "ant-api03-key...77c2"
)

var replacements = map[string]string{
	"{{OPENAI_1}}":    OpenAI1,
	"{{OPENAI_2}}":    OpenAI2,
	"{{ANTHROPIC_1}}": Anthropic1,
}

// Fill replaces {{PLACEHOLDERS}} in s with fake keys.
func Fill(s string) string {
	for k, v := range replacements {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// FixtureProject copies testdata/fixture-project into a temp dir with fake
// keys filled in and returns its path.
func FixtureProject(t testing.TB) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixture-project")
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		os.MkdirAll(filepath.Dir(out), 0o755)
		return os.WriteFile(out, []byte(Fill(string(b))), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
