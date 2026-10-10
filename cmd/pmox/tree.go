package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// The command tree: noun groups (vm, template, context, config, mount,
// key, access) are the canonical home of every operation; the commands
// people type daily are also registered at the root as shortcuts — a
// second instance of the very same constructor, so flags, completion,
// output and exit codes are identical. Old flat names stay registered
// as deprecated instances for the deprecation window.

// Root help sections.
const (
	groupStart       = "start"
	groupCommon      = "common"
	groupResources   = "resources"
	groupMaintenance = "maintenance"
)

// canonicalAnnotation records the canonical path of a shortcut.
const canonicalAnnotation = "pmox.canonical"

// paletteLeafAnnotation marks a command with subcommands that the
// palette runs directly instead of opening as a menu ('version' prints
// the version; 'version upgrade' is for the command line).
const paletteLeafAnnotation = "pmox.palette-leaf"

// rename changes the command word of c's Use (keeping its argument
// synopsis) and replaces its aliases.
func rename(c *cobra.Command, name string, aliases ...string) *cobra.Command {
	if i := strings.IndexByte(c.Use, ' '); i >= 0 {
		c.Use = name + c.Use[i:]
	} else {
		c.Use = name
	}
	c.Aliases = aliases
	return c
}

// withShort replaces c's one-line summary.
func withShort(c *cobra.Command, short string) *cobra.Command {
	c.Short = short
	return c
}

// shortcut marks c as the root-level shortcut for canonical (e.g.
// "vm list") and files it under a help section.
func shortcut(c *cobra.Command, group, canonical string) *cobra.Command {
	c.GroupID = group
	c.Short = fmt.Sprintf("%s  (%s)", c.Short, canonical)
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[canonicalAnnotation] = canonical
	return c
}

// deprecatedAnnotation records an old form's replacement.
const deprecatedAnnotation = "pmox.deprecated"

// deprecated turns c into a hidden (no help, no completion) but still
// working old form. Before running, it writes one note naming
// replacement explicitly to stderr — never stdout, so JSON output stays
// clean — and the exit code is the command's own. (cobra's built-in
// Deprecated note goes to the output writer, so it isn't used.)
func deprecated(c *cobra.Command, replacement string) *cobra.Command {
	c.Hidden = true
	if c.Annotations == nil {
		c.Annotations = map[string]string{}
	}
	c.Annotations[deprecatedAnnotation] = replacement
	note := func(cmd *cobra.Command) {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: '%s' is deprecated and will be removed — use '%s'\n", cmd.CommandPath(), replacement)
	}
	if run := c.RunE; run != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error { note(cmd); return run(cmd, args) }
	} else if run := c.Run; run != nil {
		c.Run = func(cmd *cobra.Command, args []string) { note(cmd); run(cmd, args) }
	}
	return c
}

// errGroupHelp makes a bare noun outside a terminal exit with the
// user-input code after printing the group's help (already printed, so
// main adds no "Error:" line).
type errGroupHelp struct{ group string }

func (e *errGroupHelp) Error() string { return fmt.Sprintf("'pmox %s' needs a subcommand", e.group) }
func (e *errGroupHelp) ExitCode() int { return exitcode.ExitUserError }
func (e *errGroupHelp) SelfReported() {}

// nounGroup builds a noun group: on a terminal, running it bare shows a
// picker of its verbs and runs the chosen one; otherwise it prints help
// and exits 2.
func nounGroup(name, short, long string, subs ...*cobra.Command) *cobra.Command {
	g := &cobra.Command{
		Use:   name,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !tui.Interactive() {
				_ = cmd.Help()
				return &errGroupHelp{group: name}
			}
			return runGroupMenu(cmd)
		},
	}
	g.AddCommand(subs...)
	return g
}

// runGroupMenu opens the command palette at group (e.g. "pmox › vm").
func runGroupMenu(group *cobra.Command) error {
	return runPalette(group, strings.Fields(group.CommandPath())[1:])
}

func newVMCmd() *cobra.Command {
	g := nounGroup("vm", "All VM commands", `Manage pmox VMs on the current context's cluster.

The most common verbs are also available at the top level:
pmox launch, list, info, start, stop, delete, shell, exec, cp, sync, apply.`)
	g.AddGroup(
		&cobra.Group{ID: "vm-lifecycle", Title: "Lifecycle:"},
		&cobra.Group{ID: "vm-access", Title: "Access & files:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			g.AddCommand(c)
		}
	}
	add("vm-lifecycle",
		newLaunchCmd(), newCloneCmd(), rename(newListCmd(), "list", "ls"), newInfoCmd(),
		newStartCmd(), newStopCmd(), rename(newDeleteCmd(), "delete", "rm"),
	)
	add("vm-access",
		newShellCmd(), newExecCmd(), newCpCmd(), newSyncCmd(), newApplyCmd(), newSSHConfigCmd(),
	)
	return g
}

func newTemplateCmd() *cobra.Command {
	return nounGroup("template", "Build and list templates", `Build and list the Proxmox templates pmox launches VMs from.`,
		rename(newCreateTemplateCmd(), "create"),
		newTemplateListCmd(),
	)
}

func newContextCmd() *cobra.Command {
	return nounGroup("context", "Switch between Proxmox servers", `A context is a configured server (URL, token, defaults, node SSH)
addressed by a short name. Set the current one so commands don't need
--context every time.

Examples:
  pmox context list
  pmox context add
  pmox context use prod
  pmox context rename 192.168.0.185 prod
  pmox context delete lab`,
		rename(newGetContextsCmd(), "list", "ls"),
		newContextAddCmd(),
		rename(newUseContextCmd(), "use"),
		rename(newCurrentContextCmd(), "current"),
		rename(newRenameContextCmd(), "rename"),
		rename(newDeleteContextCmd(), "delete", "rm"),
	)
}

// registerCommands builds the whole tree on root.
func registerCommands(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: groupStart, Title: "Get started:"},
		&cobra.Group{ID: groupCommon, Title: "Common:"},
		&cobra.Group{ID: groupResources, Title: "Resources:"},
		&cobra.Group{ID: groupMaintenance, Title: "Maintenance:"},
	)
	root.SetHelpCommandGroupID(groupMaintenance)
	root.SetCompletionCommandGroupID(groupMaintenance)

	initCmd := newInitCmd()
	initCmd.GroupID = groupStart
	root.AddCommand(
		initCmd,
		shortcut(newLaunchCmd(), groupStart, "vm launch"),
		shortcut(newShellCmd(), groupStart, "vm shell"),

		shortcut(rename(newListCmd(), "list", "ls"), groupCommon, "vm list"),
		shortcut(newInfoCmd(), groupCommon, "vm info"),
		shortcut(newStartCmd(), groupCommon, "vm start"),
		shortcut(newStopCmd(), groupCommon, "vm stop"),
		shortcut(rename(newDeleteCmd(), "delete", "rm"), groupCommon, "vm delete"),
		shortcut(newExecCmd(), groupCommon, "vm exec"),
		shortcut(newCpCmd(), groupCommon, "vm cp"),
		shortcut(newSyncCmd(), groupCommon, "vm sync"),
		shortcut(newApplyCmd(), groupCommon, "vm apply"),
		shortcut(newUmountCmd(), groupCommon, "mount delete"),
	)

	for _, g := range []*cobra.Command{
		newVMCmd(), newTemplateCmd(), newContextCmd(), newConfigCmd(), newMountGroupCmd(), newKeyCmd(), newAccessCmd(),
	} {
		g.GroupID = groupResources
		root.AddCommand(g)
	}

	for _, c := range []*cobra.Command{newDoctorCmd(), newCleanupCmd(), newVersionCmd()} {
		c.GroupID = groupMaintenance
		root.AddCommand(c)
	}

	// Old flat forms, kept working for the deprecation window.
	root.AddCommand(
		deprecated(newCreateTemplateCmd(), "pmox template create"),
		deprecated(newSSHConfigCmd(), "pmox vm ssh-config"),
		deprecated(newCloneCmd(), "pmox vm clone"),
		deprecatedConfigureCmd(),
	)
}
