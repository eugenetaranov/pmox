package answers

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// legacyVars are the variables the shell-format answers file may set
// (A_WIRE_CLAUDE: saved before v0.32).
var legacyVars = map[string]bool{
	"A_USER": true, "A_PICKS": true, "A_MISE_EXTRA": true, "A_MCP_LISTEN": true, "A_MCP_SERVERS": true,
	"A_WIRE_AGENTS": true, "A_GIT_NAME": true, "A_GIT_EMAIL": true, "A_TZ": true, "A_WIRE_CLAUDE": true,
}

var legacyLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)

// decodeLegacy decodes the shell-format answers file line by line, without
// running it; unknown or unsafe lines are skipped with a note.
func decodeLegacy(path, content string) (*Answers, []string) {
	vals := map[string]string{}
	var notes []string
	for no, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := legacyLine.FindStringSubmatch(line)
		if m == nil || !legacyVars[m[1]] {
			notes = append(notes, fmt.Sprintf("%s:%d: ignored (not a devbox-setup answer): %.80s", path, no+1, line))
			continue
		}
		v, err := Unquote(m[2])
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s:%d: ignored %s (%v)", path, no+1, m[1], err))
			continue
		}
		vals[m[1]] = v
	}
	if vals["A_WIRE_AGENTS"] == "" && vals["A_WIRE_CLAUDE"] != "" {
		vals["A_WIRE_AGENTS"] = vals["A_WIRE_CLAUDE"]
	}
	a := &Answers{
		Version:    Version,
		User:       vals["A_USER"],
		Picks:      strings.Fields(vals["A_PICKS"]),
		MiseExtra:  strings.Fields(vals["A_MISE_EXTRA"]),
		MCPListen:  vals["A_MCP_LISTEN"],
		MCPServers: strings.Fields(vals["A_MCP_SERVERS"]),
		Git:        Git{Name: vals["A_GIT_NAME"], Email: vals["A_GIT_EMAIL"]},
		TZ:         vals["A_TZ"],
	}
	switch vals["A_WIRE_AGENTS"] {
	case "yes":
		t := true
		a.WireAgents = &t
	case "no":
		f := false
		a.WireAgents = &f
	}
	return a, notes
}

// ErrUnsafe is returned for a value that would expand or run something.
var ErrUnsafe = errors.New("unsafe value")

var cEscapes = map[byte]byte{'n': 10, 't': 9, 'r': 13, 'a': 7, 'b': 8, 'e': 27, 'E': 27, 'f': 12, 'v': 11,
	'\\': 92, '\'': 39, '"': 34, '?': 63}

// Unquote decodes a value as bash's printf %q (or plain quoting) wrote it,
// without running anything: anything that would expand or start a command
// is an error.
func Unquote(v string) (string, error) {
	unsafe := func(why string) (string, error) { return "", fmt.Errorf("%w: %s", ErrUnsafe, why) }
	var out []byte
	n := len(v)
	for i := 0; i < n; {
		c := v[i]
		switch {
		case c == '\\':
			if i+1 < n {
				_, size := utf8.DecodeRuneInString(v[i+1:])
				out = append(out, v[i+1:i+1+size]...)
				i += 1 + size
			} else {
				i++
			}
		case c == '\'':
			j := strings.IndexByte(v[i+1:], '\'')
			if j < 0 {
				return unsafe("unterminated quote")
			}
			out = append(out, v[i+1:i+1+j]...)
			i += j + 2
		case strings.HasPrefix(v[i:], "$'"):
			i += 2
			for {
				if i >= n {
					return unsafe("unterminated $'")
				}
				c := v[i]
				if c == '\'' {
					i++
					break
				}
				if c != '\\' || i+1 >= n {
					out = append(out, c)
					i++
					continue
				}
				e := v[i+1]
				i += 2
				switch {
				case cEscapes[e] != 0:
					out = append(out, cEscapes[e])
				case e >= '0' && e <= '7':
					digits := string(e) + leading(v[i:], 2, "01234567")
					i += len(digits) - 1
					x, _ := strconv.ParseUint(digits, 8, 16)
					out = append(out, byte(x&0xFF))
				case e == 'x':
					h := leading(v[i:], 2, "0123456789abcdefABCDEF")
					if h == "" {
						return unsafe("bad \\x escape")
					}
					x, _ := strconv.ParseUint(h, 16, 8)
					out = append(out, byte(x))
					i += len(h)
				case e == 'u' || e == 'U':
					max := 4
					if e == 'U' {
						max = 8
					}
					h := leading(v[i:], max, "0123456789abcdefABCDEF")
					if h == "" {
						return unsafe("bad \\u escape")
					}
					x, _ := strconv.ParseUint(h, 16, 32)
					out = utf8.AppendRune(out, rune(x))
					i += len(h)
				default:
					out = append(out, '\\', e)
				}
			}
		case c == '"':
			i++
			for {
				if i >= n {
					return unsafe("unterminated double quote")
				}
				c := v[i]
				if c == '"' {
					i++
					break
				}
				if c == '$' || c == '`' {
					return unsafe("expansion in double quotes")
				}
				if c == '\\' && i+1 < n && strings.IndexByte("$`\"\\", v[i+1]) >= 0 {
					out = append(out, v[i+1])
					i += 2
					continue
				}
				out = append(out, c)
				i++
			}
		case strings.IndexByte("$`;&|<>(){} \t\n", c) >= 0:
			return unsafe(fmt.Sprintf("unquoted %q", c))
		default:
			out = append(out, c)
			i++
		}
	}
	return strings.ToValidUTF8(string(out), "�"), nil
}

// leading returns the longest prefix of s, at most max bytes, from set.
func leading(s string, max int, set string) string {
	i := 0
	for i < len(s) && i < max && strings.IndexByte(set, s[i]) >= 0 {
		i++
	}
	return s[:i]
}
