// Package creds holds what devbox-setup asks for that is never saved: the
// credentials typed during a run (the prompts are the catalog's cred
// steps).
// They go to the install steps (the bash installer reads them on stdin)
// and nowhere else.
package creds

import (
	"bytes"
	"sort"
)

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
	// SudoPW is a password to set for the user (sudopw), when they have none.
	SudoPW string
	// GitKey is a private key for gitkey, sent by pmox devbox gitkey.
	GitKey []byte
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
