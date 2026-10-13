package creds

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The bash installer's read_secrets gets back exactly what Encode wrote.
func TestEncodeReadByBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	b, err := os.ReadFile("../../bootstrap/devbox-setup")
	if err != nil {
		t.Skip("bash installer gone")
	}
	script := string(b)
	i := strings.Index(script, "\nread_secrets() {")
	fn := script[i+1:]
	fn = fn[:strings.Index(fn, "\n}\n")+3]
	s := New()
	s.Cred["agentkeys OPENAI_API_KEY"] = "sk-1 with space"
	s.MCP["linear LINEAR_API_KEY"] = "lin_\nnewline"
	s.RegUser["ghcr.io"], s.RegPass["ghcr.io"] = "ada", "p'w\"d"
	s.TSAuthKey, s.AgeChoice = "tskey-1", "generate"
	cmd := exec.Command(bash, "-c", "set -euo pipefail\ndeclare -A CRED=() MCP_KEYS=() REG_USER=() REG_PASS=()\nTS_AUTHKEY= GH_TOKEN_IN= GH_PASSPHRASE= AGE_CHOICE= AGE_KEY=\n"+fn+
		`read_secrets
printf '%s|%s|%s|%s|%s|%s' "${CRED[agentkeys OPENAI_API_KEY]}" "${MCP_KEYS[linear LINEAR_API_KEY]}" "${REG_USER[ghcr.io]}" "${REG_PASS[ghcr.io]}" "$TS_AUTHKEY" "$AGE_CHOICE"`)
	cmd.Stdin = strings.NewReader(string(s.Encode()))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if want := "sk-1 with space|lin_\nnewline|ada|p'w\"d|tskey-1|generate"; string(out) != want {
		t.Errorf("bash read %q, want %q", out, want)
	}
}
