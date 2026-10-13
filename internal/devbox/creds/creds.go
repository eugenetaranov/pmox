// Package creds holds what devbox-setup asks for that is never saved: the
// credentials typed during a run, and the table of credential prompts.
// They go to the install steps (the bash installer reads them on stdin)
// and nowhere else.
package creds

import (
	"bytes"
	"sort"
)

// Row is one credential prompt (the bash installer's CRED_ROWS).
type Row struct {
	Item, Key string
	// Answer is the saved answer a plain setting goes in; "" for a secret.
	Answer        string
	Prompt, How   string
	When, Default string // When: only if this item is picked or this tool installed
}

// Secret reports whether the row is a secret (read without echo, never saved).
func (r Row) Secret() bool { return r.Answer == "" }

// Rows are the credential prompts, in order.
var Rows = []Row{
	{Item: "agentkeys", Key: "ANTHROPIC_API_KEY", Prompt: "Anthropic API key (empty: log in with 'claude' instead)", How: "https://console.anthropic.com/settings/keys", When: "claude"},
	{Item: "agentkeys", Key: "OPENAI_API_KEY", Prompt: "OpenAI API key (Codex)", How: "https://platform.openai.com/api-keys", When: "codex"},
	{Item: "agentkeys", Key: "GEMINI_API_KEY", Prompt: "Gemini API key", How: "https://aistudio.google.com/apikey", When: "gemini"},
	{Item: "npmrc", Key: "NPM_SCOPE", Answer: "A_NPM_SCOPE", Prompt: "npm scope (e.g. @acme)"},
	{Item: "npmrc", Key: "NPM_REGISTRY", Answer: "A_NPM_REGISTRY", Prompt: "npm registry URL", Default: "https://npm.pkg.github.com"},
	{Item: "npmrc", Key: "NPM_TOKEN", Prompt: "npm registry token", How: "for GitHub Packages: a token with read:packages at https://github.com/settings/tokens"},
}

// Secrets are a run's typed secrets.
type Secrets struct {
	Cred      map[string]string // "item KEY"
	MCP       map[string]string // "server KEY"
	RegUser   map[string]string // registry → username
	RegPass   map[string]string // registry → password or token
	TSAuthKey string
	GHToken   string
	// GHPassphrase is "yes" to set a passphrase on a new key (ssh-keygen
	// asks for it itself).
	GHPassphrase      string
	AgeChoice, AgeKey string
}

// New returns empty secrets.
func New() *Secrets {
	return &Secrets{Cred: map[string]string{}, MCP: map[string]string{}, RegUser: map[string]string{}, RegPass: map[string]string{}}
}

// Encode is the stdin the bash installer's --secrets reads: NUL-separated
// name/value pairs.
func (s *Secrets) Encode() []byte {
	var b bytes.Buffer
	put := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(v)
		b.WriteByte(0)
	}
	for _, m := range []struct {
		prefix string
		vals   map[string]string
	}{{"CRED:", s.Cred}, {"MCP:", s.MCP}, {"REG_USER:", s.RegUser}, {"REG_PASS:", s.RegPass}} {
		keys := make([]string, 0, len(m.vals))
		for k := range m.vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			put(m.prefix+k, m.vals[k])
		}
	}
	put("TS_AUTHKEY", s.TSAuthKey)
	put("GH_TOKEN_IN", s.GHToken)
	put("GH_PASSPHRASE", s.GHPassphrase)
	put("AGE_CHOICE", s.AgeChoice)
	put("AGE_KEY", s.AgeKey)
	return b.Bytes()
}
