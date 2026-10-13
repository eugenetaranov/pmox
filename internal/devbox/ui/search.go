// Package ui is devbox-setup's terminal UI: the tabbed picker with
// type-to-search across every tab and mise's registry, and the prompts.
package ui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// MiseTab is the pseudo-tab of mise registry tools (search results only).
const MiseTab = "__mise__"

// MaxResults caps the search results.
const MaxResults = 200

// Entry is one row the picker can show: a catalog item or a mise tool.
type Entry struct {
	Tab, ID, Label, Source string
	On                     bool
	// Names and Text are set for mise tools: the name and aliases, and
	// the full description.
	Names []string
	Text  string
}

var (
	shortcode = regexp.MustCompile(`:[a-z0-9_+-]+:`)
	sentence  = regexp.MustCompile(`^(.+?[.!?])(\s|$)`)
	wordRe    = regexp.MustCompile(`[\w.+-]+`)
)

// Narrow drops double-width characters (emoji, CJK) and :emoji: shortcodes,
// which break the column layout.
func Narrow(s string) string {
	s = shortcode.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		k := width.LookupRune(r).Kind()
		if k == width.EastAsianWide || k == width.EastAsianFullwidth || unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func firstSentence(s string) string {
	if m := sentence.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	return strings.TrimRight(s, ".")
}

// LoadMise parses the registry TSV (name, comma-separated aliases,
// description per line).
func LoadMise(tsv string) []*Entry {
	var out []*Entry
	for _, line := range strings.Split(tsv, "\n") {
		p := strings.Split(line, "\t")
		if len(p) < 3 || p[0] == "" {
			continue
		}
		names := []string{p[0]}
		for _, a := range strings.Split(p[1], ",") {
			if a != "" {
				names = append(names, a)
			}
		}
		label := p[0]
		if sum := Narrow(firstSentence(p[2])); sum != "" {
			label = p[0] + " — " + sum
		}
		out = append(out, &Entry{Tab: MiseTab, ID: "mise:" + p[0], Label: label, Source: "mise use -g " + p[0], Names: names, Text: p[2]})
	}
	return out
}

// RegistryTSV converts `mise registry -J` output to the LoadMise TSV.
func RegistryTSV(js []byte) string {
	var tools []struct {
		Short       string   `json:"short"`
		Aliases     []string `json:"aliases"`
		Description string   `json:"description"`
	}
	if json.Unmarshal(js, &tools) != nil {
		return ""
	}
	clean := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var b strings.Builder
	for _, t := range tools {
		if clean(t.Short) == "" {
			continue
		}
		al := make([]string, len(t.Aliases))
		for i, a := range t.Aliases {
			al[i] = clean(a)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", clean(t.Short), strings.Join(al, ","), clean(t.Description))
	}
	return b.String()
}

// toolName: "pipx:aider-chat[x]@latest" → "aider-chat".
func toolName(spec string) string {
	spec = strings.SplitN(spec, "@", 2)[0]
	spec = strings.SplitN(spec, "[", 2)[0]
	if i := strings.LastIndexByte(spec, ':'); i >= 0 {
		spec = spec[i+1:]
	}
	if i := strings.LastIndexByte(spec, '/'); i >= 0 {
		spec = spec[i+1:]
	}
	return strings.ToLower(spec)
}

// MiseTools returns the tools a source installs with mise ("mise use -g a
// b" or "mise pipx:x", between "·" separators).
func MiseTools(source string) map[string]bool {
	out := map[string]bool{}
	for _, seg := range strings.Split(source, "·") {
		w := strings.Fields(seg)
		if len(w) == 0 || w[0] != "mise" {
			continue
		}
		if len(w) >= 3 && w[1] == "use" && w[2] == "-g" {
			w = w[3:]
		} else {
			w = w[1:]
		}
		for _, t := range w {
			out[toolName(t)] = true
		}
	}
	return out
}

// Dedupe drops mise registry rows for tools a catalog item already installs.
func Dedupe(entries []*Entry) []*Entry {
	covered := map[string]bool{}
	for _, e := range entries {
		if e.Tab != MiseTab {
			for t := range MiseTools(e.Source) {
				covered[t] = true
			}
		}
	}
	var out []*Entry
	for _, e := range entries {
		dup := false
		if e.Tab == MiseTab {
			for _, n := range e.Names {
				dup = dup || covered[strings.ToLower(n)]
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	return out
}

func alnum(b byte) bool {
	r := rune(b)
	return r >= 0x80 || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// fuzzy scores q as a substring, else (when allowed) an in-order
// subsequence, of s (both lower case); ok=false when it doesn't match.
func fuzzy(q, s string, subsequence bool) (int, bool) {
	l := len(s)
	if l > 200 {
		l = 200
	}
	if i := strings.Index(s, q); i >= 0 {
		if i == 0 || !alnum(s[i-1]) {
			return 300 - l/20, true
		}
		return 200 - l/20, true
	}
	if !subsequence {
		return 0, false
	}
	pos, prev, score := 0, -2, 0
	for i := 0; i < len(q); i++ {
		j := strings.IndexByte(s[pos:], q[i])
		if j < 0 {
			return 0, false
		}
		j += pos
		if j == prev+1 {
			score += 5
		}
		if j == 0 || !alnum(s[j-1]) {
			score += 8
		}
		prev, pos = j, j+1
	}
	return score - l/20, true
}

func namesOf(e *Entry) (primary, secondary []string) {
	if e.Names != nil {
		for i, n := range e.Names {
			if i == 0 {
				primary = append(primary, strings.ToLower(n))
			} else {
				secondary = append(secondary, strings.ToLower(n))
			}
		}
		return
	}
	secondary = wordRe.FindAllString(strings.ToLower(e.Label), -1)
	var tools []string
	for t := range MiseTools(e.Source) {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	return []string{strings.ToLower(e.ID)}, append(secondary, tools...)
}

func nameScore(q, n string) (int, bool) {
	switch {
	case n == q:
		return 1000, true
	case strings.HasPrefix(n, q):
		return 900 - len(n), true
	case strings.Contains(n, q):
		return 600 - len(n), true
	}
	return 0, false
}

// Score ranks e for q (higher is better; ok=false: no match). Primary
// names first (exact, prefix, substring), then secondary names, then a
// fuzzy match on the label (a mise tool's names), then the source or
// description as a substring. Catalog items rank ahead of mise tools.
func Score(e *Entry, q string) (int, bool) {
	q = strings.ToLower(q)
	primary, secondary := namesOf(e)
	best, ok := 0, false
	take := func(s int, m bool) {
		if m && (!ok || s > best) {
			best, ok = s, true
		}
	}
	for _, n := range primary {
		take(nameScore(q, n))
	}
	for _, n := range secondary {
		if s, m := nameScore(q, n); m {
			take(s-150, true)
		}
	}
	if e.Tab == MiseTab {
		for _, n := range append(append([]string{}, primary...), secondary...) {
			take(fuzzy(q, n, true))
		}
	} else {
		take(fuzzy(q, strings.ToLower(e.Label), true))
	}
	text := e.Source
	if e.Text != "" {
		text = e.Text
	}
	if s, m := fuzzy(q, strings.ToLower(text), false); m {
		take(s-50, true)
	}
	if !ok {
		return 0, false
	}
	if e.Tab != MiseTab {
		best += 200
	}
	return best, true
}

// Search returns the entries matching q, best first.
func Search(entries []*Entry, q string) []*Entry {
	type hit struct {
		s, n int
		e    *Entry
	}
	var hits []hit
	for n, e := range entries {
		if s, ok := Score(e, q); ok {
			hits = append(hits, hit{s, n, e})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.s != b.s {
			return a.s > b.s
		}
		if (a.e.Tab == MiseTab) != (b.e.Tab == MiseTab) {
			return b.e.Tab == MiseTab
		}
		if len(a.e.Label) != len(b.e.Label) {
			return len(a.e.Label) < len(b.e.Label)
		}
		return a.n < b.n
	})
	if len(hits) > MaxResults {
		hits = hits[:MaxResults]
	}
	out := make([]*Entry, len(hits))
	for i, h := range hits {
		out[i] = h.e
	}
	return out
}
