package bootstrap

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Hostname returns an edit that names the VM after name in its cloud-init:
// hostname (name's first label; fqdn too when name has dots) and
// manage_etc_hosts, so the name resolves locally (no "sudo: unable to
// resolve host"). Keys the user-data already sets are kept.
func Hostname(name string) func([]byte) ([]byte, error) {
	return func(userData []byte) ([]byte, error) {
		return edit(userData, func(root *yaml.Node) error {
			host, _, dotted := strings.Cut(name, ".")
			if hasKey(root, "hostname") || hasKey(root, "fqdn") {
				return nil
			}
			setScalar(root, "hostname", host)
			if dotted {
				setScalar(root, "fqdn", name)
			}
			if !hasKey(root, "manage_etc_hosts") {
				root.Content = append(root.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "manage_etc_hosts"},
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
			}
			return nil
		})
	}
}

func hasKey(root *yaml.Node, key string) bool {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return true
		}
	}
	return false
}

// setScalar appends key: value (a string) to root.
func setScalar(root *yaml.Node, key, value string) {
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
