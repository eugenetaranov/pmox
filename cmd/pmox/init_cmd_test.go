package main

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/exitcode"
)

func TestRunInitModesAreMutuallyExclusive(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	err := runInit(cmd, &initFlags{list: true, remove: "https://pve.lan:8006"})
	if !errors.Is(err, exitcode.ErrUserInput) {
		t.Fatalf("err = %v, want ErrUserInput", err)
	}
	if got := exitcode.From(err); got != exitcode.ExitUserError {
		t.Errorf("exit code = %d, want %d", got, exitcode.ExitUserError)
	}
}
