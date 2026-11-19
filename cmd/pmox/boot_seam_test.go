package main

import (
	"context"
	"io"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// Tests never reach a guest agent for the boot or host-key checks.
func init() {
	waitForLoginsFn = func(_ context.Context, _ io.Writer, _ *pveclient.Client, _ *vm.Ref) {}
	refreshGuestHostKeyFn = func(_ context.Context, _ io.Writer, _ *pveclient.Client, _ *vm.Ref, _ string) {}
}
