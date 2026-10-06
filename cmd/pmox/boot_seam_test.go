package main

import (
	"context"
	"io"

	"github.com/eugenetaranov/pmox/internal/pveclient"
	"github.com/eugenetaranov/pmox/internal/vm"
)

// Tests never reach a guest agent for the boot check.
func init() {
	waitForLoginsFn = func(_ context.Context, _ io.Writer, _ *pveclient.Client, _ *vm.Ref) {}
}
