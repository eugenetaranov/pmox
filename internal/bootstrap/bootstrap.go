// Package bootstrap ships devbox-setup — an interactive on-node installer
// (dev tools, Docker, mise, Claude Code, MCPJungle + MCP servers) — onto
// every VM pmox launches, by adding it to the VM's cloud-init write_files.
//
// The script lives here; its assets (mcp-sync, the MCP catalog, the zsh/vim
// templates) are copies from github.com/tackhq/tack-roles, refreshed with
// `task bootstrap:sync`, so the on-node installer and the tack roles share
// the same files.
package bootstrap

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed devbox-setup assets
var files embed.FS

// ShareDir is where the assets land on the VM; the script reads them there.
const ShareDir = "/usr/local/share/devbox-setup"

// ScriptPath is where the script lands on the VM.
const ScriptPath = "/usr/local/bin/devbox-setup"

// File is one write_files entry.
type File struct {
	Path        string
	Permissions string
	Content     []byte
}

// Files returns the script and its assets with their on-VM paths.
func Files() []File {
	script, err := files.ReadFile("devbox-setup")
	if err != nil {
		panic(err) // embedded; cannot fail
	}
	out := []File{{Path: ScriptPath, Permissions: "0755", Content: script}}
	_ = fs.WalkDir(files, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := files.ReadFile(p)
		perm := "0644"
		if path.Base(p) == "mcp-sync" {
			perm = "0755"
		}
		out = append(out, File{Path: path.Join(ShareDir, strings.TrimPrefix(p, "assets/")), Permissions: perm, Content: b})
		return nil
	})
	return out
}

// Inject returns userData with the devbox-setup files appended to its
// write_files list (created when absent). Everything else is kept as is.
// userData must be a cloud-config mapping document.
func Inject(userData []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(userData, &doc); err != nil {
		return nil, fmt.Errorf("parse cloud-init: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cloud-init is not a YAML mapping")
	}
	root := doc.Content[0]

	var list *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "write_files" {
			list = root.Content[i+1]
			break
		}
	}
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "write_files"}, list)
	} else if list.Kind != yaml.SequenceNode {
		if list.Tag == "!!null" {
			list.Kind, list.Tag, list.Value = yaml.SequenceNode, "", ""
		} else {
			return nil, fmt.Errorf("cloud-init write_files is not a list")
		}
	}

	for _, f := range Files() {
		enc, err := gzipB64(f.Content)
		if err != nil {
			return nil, err
		}
		entry := &yaml.Node{Kind: yaml.MappingNode}
		for _, kv := range [][2]string{
			{"path", f.Path},
			{"permissions", f.Permissions},
			{"owner", "root:root"},
			{"encoding", "gz+b64"},
			{"content", enc},
		} {
			v := &yaml.Node{Kind: yaml.ScalarNode, Value: kv[1]}
			if kv[0] == "permissions" {
				v.Style = yaml.SingleQuotedStyle // keep the leading 0
			}
			entry.Content = append(entry.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: kv[0]}, v)
		}
		list.Content = append(list.Content, entry)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode cloud-init: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	// cloud-init only treats the file as cloud-config with this first line.
	if !bytes.HasPrefix(out, []byte("#cloud-config")) {
		out = append([]byte("#cloud-config\n"), out...)
	}
	return out, nil
}

func gzipB64(b []byte) (string, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(b); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
