package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/tui"
)

// 'pmox init' is first-run setup. On a machine that is already set up it
// doesn't start over at the URL prompt; it asks what the user meant:
// change a configured server, add another one, or remove one. The same
// actions exist as direct commands: 'pmox config edit',
// 'pmox context add', 'pmox context delete'.

// setupChoiceFn is a seam over the chooser.
var setupChoiceFn = tui.Select

func runInitConfigured(ctx context.Context, p prompter, cfg *config.Config) error {
	contexts := cfg.Contexts()
	names := make([]string, 0, len(contexts))
	current := contexts[0].Name
	for _, c := range contexts {
		names = append(names, c.Name)
		if c.Current {
			current = c.Name
		}
	}
	editLabel := "Change settings of " + current + "  (pmox config edit)"
	if len(contexts) > 1 {
		editLabel = "Change settings of a configured server  (pmox config edit)"
	}
	choice, err := setupChoiceFn(
		fmt.Sprintf("pmox is already set up for %s. What would you like to do?", strings.Join(names, ", ")),
		[]huh.Option[string]{
			huh.NewOption(editLabel, "edit"),
			huh.NewOption("Add another Proxmox server  (pmox context add)", "add"),
			huh.NewOption("Remove a server  (pmox context delete)", "remove"),
			huh.NewOption("Cancel", "cancel"),
		})
	if err != nil {
		return err
	}
	switch choice {
	case "edit":
		name := current
		if len(contexts) > 1 {
			if name, err = pickContext(cfg); err != nil {
				return err
			}
		}
		c, _ := cfg.ContextByName(name)
		return runEditForm(ctx, p, cfg, c.URL)
	case "add":
		return runInteractiveForm(ctx, p)
	case "remove":
		name, err := pickContext(cfg)
		if err != nil {
			return err
		}
		c, _ := cfg.ContextByName(name)
		ok, err := confirmRemoveFn(fmt.Sprintf("Remove %s (%s) and its stored secrets?", c.Name, c.URL), false)
		if err != nil {
			return err
		}
		if !ok {
			p.Printf("nothing removed\n")
			return nil
		}
		return runRemove(p, c.URL)
	default:
		return nil
	}
}

// confirmRemoveFn is a seam over the removal confirmation.
var confirmRemoveFn = tui.Confirm

// newContextAddCmd runs the setup wizard for an additional server.
func newContextAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add another Proxmox server",
		Long: `Run the setup wizard for an additional Proxmox server. It becomes a new
context; switch between servers with 'pmox context use'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := newStdPrompter(ctx)
			if interactiveFn() {
				return runInteractiveForm(ctx, p)
			}
			return runInteractiveLinear(ctx, p)
		},
	}
}
