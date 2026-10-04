package site

import (
	"strconv"
	"strings"
	"unicode"
)

// slugger generates heading IDs the way GitHub does (github-slugger), so
// cross-page "#anchor" links written for GitHub keep working in the HTML site.
type slugger struct {
	seen map[string]int
}

func newSlugger() *slugger {
	return &slugger{seen: map[string]int{}}
}

// slug lowercases the heading text, drops everything except letters, marks,
// numbers, connector punctuation, spaces and hyphens, and turns each space
// into a hyphen. Runs of hyphens are not collapsed, matching GitHub.
// Repeated slugs on one page get "-1", "-2", ... suffixes.
func (s *slugger) slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-', unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.Pc):
			b.WriteRune(r)
		}
	}
	base := b.String()
	slug := base
	for {
		if _, taken := s.seen[slug]; !taken {
			break
		}
		s.seen[base]++
		slug = base + "-" + strconv.Itoa(s.seen[base])
	}
	s.seen[slug] = 0
	return slug
}
