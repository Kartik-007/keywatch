package core

import "regexp"

// Patterns are deliberately few and readable: this file is the whole
// detector, so anyone can audit what keywatch looks for.
var patterns = []struct {
	provider string
	re       *regexp.Regexp
}{
	{Anthropic, regexp.MustCompile(`sk-ant-(?:api|admin)\d\d-[A-Za-z0-9_-]{20,}`)},
	{OpenAI, regexp.MustCompile(`sk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{20,}`)},
	// Legacy user keys: sk- + 48 alphanumerics with the "T3BlbkFJ" marker.
	{OpenAI, regexp.MustCompile(`sk-[A-Za-z0-9]{20}T3BlbkFJ[A-Za-z0-9]{20}`)},
}

// Match is one secret found inside a blob of text.
type Match struct {
	Provider string
	Value    string
}

// Detect returns every AI key in text, deduplicated, in order of appearance.
func Detect(text string) []Match {
	seen := map[string]bool{}
	var out []Match
	for _, p := range patterns {
		for _, v := range p.re.FindAllString(text, -1) {
			if !seen[v] {
				seen[v] = true
				out = append(out, Match{p.provider, v})
			}
		}
	}
	return out
}

// IsAdminKey reports whether a secret is a provider admin key, which is far
// more dangerous to leak than a normal key.
func IsAdminKey(secret string) bool {
	return regexp.MustCompile(`^sk-(?:ant-)?admin`).MatchString(secret)
}
