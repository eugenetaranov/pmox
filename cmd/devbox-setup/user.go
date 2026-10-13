package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eugenetaranov/pmox/internal/devbox/steps"
)

// sudoersDir is where a created user's sudo rule goes.
var sudoersDir = "/etc/sudoers.d"

// createUser adds the account devbox-setup was asked to set up, like
// pmox's cloud-init user: a home, bash, passwordless sudo, and the
// supplementary groups of like (the user who ran sudo, normally that
// cloud-init user). It has no password and no SSH key.
func createUser(ctx context.Context, x steps.Execer, name, like string) error {
	args := []string{"-m", "-U", "-s", "/bin/bash"}
	if like != "" && like != "root" && like != name {
		// id -nG lists the primary group first; the new user gets its own.
		if out, err := x.Run(ctx, steps.Cmd{Name: "id", Args: []string{"-nG", like}}); err == nil {
			if groups := strings.Fields(string(out)); len(groups) > 1 {
				args = append(args, "-G", strings.Join(groups[1:], ","))
			}
		}
	}
	if out, err := x.Run(ctx, steps.Cmd{Name: "useradd", Args: append(args, name)}); err != nil {
		return fmt.Errorf("useradd %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	rule := filepath.Join(sudoersDir, "90-devbox-"+name)
	if err := os.WriteFile(rule, []byte(name+" ALL=(ALL) NOPASSWD:ALL\n"), 0o440); err != nil {
		return fmt.Errorf("sudo rule for %s: %w", name, err)
	}
	return nil
}
