package watch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// parseYAML parses the block-style subset of YAML the workflow uses
// (mappings, sequences, plain/quoted scalars, literal blocks, comments) and
// rejects anything outside it, so the golden test proves the file is valid
// YAML without adding a dependency. It returns map[string]any, []any, string
// or nil.
func parseYAML(src string) (any, error) {
	var lines []yline
	for n, raw := range strings.Split(src, "\n") {
		if strings.ContainsRune(raw, '\t') {
			return nil, fmt.Errorf("line %d: tab character", n+1)
		}
		text := strings.TrimRight(raw, " ")
		trimmed := strings.TrimLeft(text, " ")
		lines = append(lines, yline{n + 1, len(text) - len(trimmed), trimmed})
	}
	p := &yparser{lines: lines}
	p.skip()
	if p.i >= len(p.lines) {
		return nil, nil
	}
	v, err := p.node(p.lines[p.i].indent)
	if err != nil {
		return nil, err
	}
	p.skip()
	if p.i < len(p.lines) {
		return nil, fmt.Errorf("line %d: unexpected indentation", p.lines[p.i].n)
	}
	return v, nil
}

type yline struct {
	n, indent int
	text      string
}

type yparser struct {
	lines []yline
	i     int
}

// skip moves past blank and comment-only lines.
func (p *yparser) skip() {
	for p.i < len(p.lines) && (p.lines[p.i].text == "" || strings.HasPrefix(p.lines[p.i].text, "#")) {
		p.i++
	}
}

func (p *yparser) node(indent int) (any, error) {
	l := p.lines[p.i]
	if l.indent != indent {
		return nil, fmt.Errorf("line %d: bad indentation", l.n)
	}
	if l.text == "-" || strings.HasPrefix(l.text, "- ") {
		return p.seq(indent)
	}
	return p.mapping(indent)
}

func (p *yparser) seq(indent int) ([]any, error) {
	var out []any
	for p.skip(); p.i < len(p.lines); p.skip() {
		l := p.lines[p.i]
		if l.indent != indent || !(l.text == "-" || strings.HasPrefix(l.text, "- ")) {
			break
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(l.text, "-"), " ")
		if rest == "" {
			p.i++
			p.skip()
			if p.i >= len(p.lines) || p.lines[p.i].indent <= indent {
				out = append(out, nil)
				continue
			}
			v, err := p.node(p.lines[p.i].indent)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		// "- key: value" starts a mapping indented past the dash.
		inner := indent + len(l.text) - len(rest)
		if isMapLine(rest) {
			p.lines[p.i] = yline{l.n, inner, rest}
			v, err := p.mapping(inner)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		v, err := scalar(rest, l.n)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.i++
	}
	return out, nil
}

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func isMapLine(s string) bool {
	k, _, ok := splitKey(s)
	return ok && keyRe.MatchString(k)
}

func splitKey(s string) (key, rest string, ok bool) {
	if strings.HasSuffix(s, ":") && !strings.Contains(s, ": ") {
		return strings.TrimSuffix(s, ":"), "", true
	}
	i := strings.Index(s, ": ")
	if i < 0 {
		return "", "", false
	}
	return s[:i], strings.TrimLeft(s[i+2:], " "), true
}

func (p *yparser) mapping(indent int) (map[string]any, error) {
	out := map[string]any{}
	for p.skip(); p.i < len(p.lines); p.skip() {
		l := p.lines[p.i]
		if l.indent < indent {
			break
		}
		if l.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation", l.n)
		}
		if strings.HasPrefix(l.text, "- ") || l.text == "-" {
			break
		}
		key, rest, ok := splitKey(l.text)
		if !ok || !keyRe.MatchString(key) {
			return nil, fmt.Errorf("line %d: expected \"key: value\", got %q", l.n, l.text)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", l.n, key)
		}
		p.i++
		switch {
		case rest == "|" || rest == "|-":
			var body []string
			for p.i < len(p.lines) && (p.lines[p.i].text == "" || p.lines[p.i].indent > indent) {
				body = append(body, p.lines[p.i].text)
				p.i++
			}
			out[key] = strings.Join(body, "\n")
		case rest == "":
			p.skip()
			if p.i < len(p.lines) {
				next := p.lines[p.i]
				isDash := next.text == "-" || strings.HasPrefix(next.text, "- ")
				if next.indent > indent || (next.indent == indent && isDash) {
					v, err := p.node(next.indent)
					if err != nil {
						return nil, err
					}
					out[key] = v
					continue
				}
			}
			out[key] = nil
		default:
			v, err := scalar(rest, l.n)
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
	}
	return out, nil
}

func scalar(s string, n int) (string, error) {
	switch s[0] {
	case '"':
		v, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("line %d: bad double-quoted string %s", n, s)
		}
		return v, nil
	case '\'':
		if len(s) < 2 || !strings.HasSuffix(s, "'") {
			return "", fmt.Errorf("line %d: unterminated single-quoted string", n)
		}
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}
	if strings.ContainsAny(s[:1], "{}[]&*!|>%@`,?:-#") {
		return "", fmt.Errorf("line %d: plain scalar can't start with %q: %s", n, s[:1], s)
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") {
		return "", fmt.Errorf("line %d: plain scalar needs quoting: %s", n, s)
	}
	return s, nil
}
