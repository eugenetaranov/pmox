package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// bannedHelpPhrases are ways help text drifts: comparing with earlier
// versions, naming API endpoints or internal limits, retired names.
var bannedHelpPhrases = []string{
	"as before", "same as always", "exactly as", "still an error", "configure offers",
	"/status/", "api2/json", "'p' for password", "capped at", "pmox configure",
}

// Help text conventions (openspec/specs/cli-command-tree): every command
// that does something has runnable examples, and help describes current
// behaviour only.
func TestHelpTextConventions(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || c.Annotations[deprecatedAnnotation] != "" || c.Name() == "help" || c.Name() == "completion" || strings.HasPrefix(c.Name(), "__") {
			return
		}
		path := c.CommandPath()
		leaf := !c.HasAvailableSubCommands()
		if (leaf || path == "pmox mount") && c.Example == "" {
			t.Errorf("%s has no Examples section", path)
		}
		for _, line := range strings.Split(c.Example, "\n") {
			line = strings.TrimSpace(line)
			if i := strings.Index(line, "#"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			words := strings.Fields(line)
			for len(words) > 0 && strings.Contains(words[0], "=") { // VAR=value pmox …
				words = words[1:]
			}
			if len(words) < 2 || words[0] != "pmox" {
				if line != "" {
					t.Errorf("%s: example %q doesn't start with 'pmox <command>'", path, line)
				}
				continue
			}
			if found, _, err := rootCmd.Find(words[1:]); err != nil || found == rootCmd || found.Annotations[deprecatedAnnotation] != "" {
				t.Errorf("%s: example %q names no current command", path, line)
			}
		}
		for _, b := range bannedHelpPhrases {
			if strings.Contains(c.Long+" "+c.Short, b) {
				t.Errorf("%s: help contains %q", path, b)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	for _, c := range rootCmd.Commands() {
		walk(c)
	}
}

// A subcommand's help points at the root for the global flags instead
// of repeating them; the root lists them.
func TestHelpDoesNotRepeatGlobalFlags(t *testing.T) {
	sub := findPath(t, "vm shell").UsageString()
	if strings.Contains(sub, "Global Flags:") || strings.Contains(sub, "--no-input") && !strings.Contains(sub, "see 'pmox --help'") {
		t.Errorf("subcommand help repeats the global flags:\n%s", sub)
	}
	if !strings.Contains(sub, "see 'pmox --help'") || !strings.Contains(sub, "--forward-agent") {
		t.Errorf("subcommand help should list its own flags and point at 'pmox --help':\n%s", sub)
	}
	root := rootCmd.UsageString()
	for _, f := range []string{"--context", "--output", "--no-input", "--no-color"} {
		if !strings.Contains(root, f) {
			t.Errorf("root help lacks %s", f)
		}
	}
}
