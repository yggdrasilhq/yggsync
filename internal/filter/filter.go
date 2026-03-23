package filter

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"yggsync/internal/config"
)

type rule struct {
	include bool
	matcher matcher
}

type matcher struct {
	raw      string
	hasSlash bool
	re       *regexp.Regexp
}

type Matcher struct {
	includeMatchers []matcher
	excludeMatchers []matcher
	rules           []rule
}

func New(job config.Job) (*Matcher, error) {
	m := &Matcher{}
	if len(job.FilterRules) > 0 {
		for _, raw := range job.FilterRules {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			include := true
			switch raw[0] {
			case '+':
				raw = strings.TrimSpace(raw[1:])
			case '-':
				include = false
				raw = strings.TrimSpace(raw[1:])
			default:
				return nil, fmt.Errorf("unsupported filter rule %q: use + or - prefixes", raw)
			}
			cm, err := compile(raw)
			if err != nil {
				return nil, err
			}
			m.rules = append(m.rules, rule{include: include, matcher: cm})
		}
		return m, nil
	}
	for _, raw := range job.Include {
		cm, err := compile(raw)
		if err != nil {
			return nil, err
		}
		m.includeMatchers = append(m.includeMatchers, cm)
	}
	for _, raw := range job.Exclude {
		cm, err := compile(raw)
		if err != nil {
			return nil, err
		}
		m.excludeMatchers = append(m.excludeMatchers, cm)
	}
	return m, nil
}

func (m *Matcher) Match(rel string) bool {
	rel = normalize(rel)
	if rel == "." {
		return true
	}
	if len(m.rules) > 0 {
		allowed := true
		for _, rule := range m.rules {
			if rule.matcher.match(rel) {
				allowed = rule.include
			}
		}
		return allowed
	}

	if len(m.includeMatchers) > 0 {
		matched := false
		for _, inc := range m.includeMatchers {
			if inc.match(rel) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, exc := range m.excludeMatchers {
		if exc.match(rel) {
			return false
		}
	}
	return true
}

func compile(pattern string) (matcher, error) {
	pattern = normalize(pattern)
	if pattern == "" {
		return matcher{}, fmt.Errorf("empty pattern")
	}
	re, err := regexp.Compile(globToRegex(pattern))
	if err != nil {
		return matcher{}, err
	}
	return matcher{
		raw:      pattern,
		hasSlash: strings.Contains(pattern, "/"),
		re:       re,
	}, nil
}

func (m matcher) match(rel string) bool {
	if !m.hasSlash {
		return m.re.MatchString(path.Base(rel))
	}
	return m.re.MatchString(rel)
}

func normalize(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p
}

func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '\\':
			b.WriteByte('\\')
			b.WriteByte(glob[i])
		case '[':
			j := i + 1
			for j < len(glob) && glob[j] != ']' {
				j++
			}
			if j >= len(glob) {
				b.WriteString(`\[`)
				continue
			}
			b.WriteString(glob[i : j+1])
			i = j
		default:
			b.WriteByte(glob[i])
		}
	}
	b.WriteString("$")
	return b.String()
}
