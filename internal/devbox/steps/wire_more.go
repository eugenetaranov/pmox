package steps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// The gateway in OpenCode (~/.config/opencode/opencode.json, "mcp") and
// Goose (~/.config/goose/config.yaml, "extensions"), each file's other
// settings kept.
const (
	openCodeConfig = "~/.config/opencode/opencode.json"
	gooseConfig    = "~/.config/goose/config.yaml"
)

func readOrEmpty(e *Env, p string) ([]byte, error) {
	b, err := os.ReadFile(e.Path(p))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// openCodeWired returns OpenCode's config with the mcpjungle remote server
// at url, and whether that changes it.
func openCodeWired(cur []byte, url string) ([]byte, bool, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(cur)) > 0 {
		if err := json.Unmarshal(cur, &doc); err != nil {
			return nil, false, fmt.Errorf("%s isn't plain JSON (comments?): add the mcpjungle server yourself: %w", openCodeConfig, err)
		}
	} else {
		doc["$schema"] = "https://opencode.ai/config.json"
	}
	servers, _ := doc["mcp"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if s, _ := servers["mcpjungle"].(map[string]any); s != nil && s["url"] == url {
		return cur, false, nil
	}
	servers["mcpjungle"] = map[string]any{"type": "remote", "url": url, "enabled": true}
	doc["mcp"] = servers
	b, err := json.MarshalIndent(doc, "", "  ")
	return append(b, '\n'), true, err
}

// gooseWired returns Goose's config with the mcpjungle extension at url,
// and whether that changes it. It edits the YAML tree, so comments and the
// order of the rest stay.
func gooseWired(cur []byte, url string) ([]byte, bool, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(cur)) > 0 {
		if err := yaml.Unmarshal(cur, &doc); err != nil {
			return nil, false, fmt.Errorf("%s: %w", gooseConfig, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("%s: not a mapping", gooseConfig)
	}
	exts := mapGet(root, "extensions")
	if exts == nil || exts.Kind != yaml.MappingNode {
		exts = &yaml.Node{Kind: yaml.MappingNode}
		mapSet(root, "extensions", exts)
	}
	if ext := mapGet(exts, "mcpjungle"); ext != nil {
		if uri := mapGet(ext, "uri"); uri != nil && uri.Value == url {
			return cur, false, nil
		}
	}
	var ext yaml.Node
	if err := ext.Encode(map[string]any{
		"enabled": true, "name": "mcpjungle", "type": "streamable_http", "uri": url, "timeout": 300,
		"description": "MCPJungle gateway (devbox-setup): every MCP server registered in it",
	}); err != nil {
		return nil, false, err
	}
	mapSet(exts, "mcpjungle", &ext)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, false, err
	}
	return out.Bytes(), true, enc.Close()
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

// writeAgentConfig writes an agent's config file as the user's, keeping
// its mode.
func writeAgentConfig(e *Env, p string, b []byte) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(e.Path(p)); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := p[:len(p)-len("/"+lastElem(p))]
	if err := (mcpWireStep{}).userDir(e, "~/.config"); err != nil {
		return err
	}
	if err := (mcpWireStep{}).userDir(e, dir); err != nil {
		return err
	}
	if err := writeFile(e.Path(p), b, mode); err != nil {
		return err
	}
	return e.chown(e.Path(p), e.UID, e.GID)
}

func lastElem(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
