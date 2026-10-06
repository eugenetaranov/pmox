package vmwait

import (
	"context"
	"errors"
	"testing"
	"time"
)

type nologinFake struct {
	reads  int
	gone   int // read number from which the file is gone
	failed error
}

func (f *nologinFake) AgentFileRead(context.Context, string, int, string) ([]byte, bool, error) {
	f.reads++
	if f.failed != nil {
		return nil, false, f.failed
	}
	if f.reads >= f.gone {
		return nil, false, errors.New("api error: 500: guest-file-open: No such file or directory")
	}
	return nil, false, nil
}

func TestWaitForBoot(t *testing.T) {
	ctx := context.Background()
	f := &nologinFake{gone: 3}
	waited, err := WaitForBoot(ctx, f, "p0", 100, time.Second, WithPollInterval(time.Millisecond))
	if err != nil || !waited || f.reads != 3 {
		t.Fatalf("booting guest: waited=%v err=%v reads=%d", waited, err, f.reads)
	}
	f = &nologinFake{gone: 1}
	if waited, err := WaitForBoot(ctx, f, "p0", 100, time.Second); err != nil || waited {
		t.Fatalf("booted guest: waited=%v err=%v", waited, err)
	}
	f = &nologinFake{failed: errors.New("403 forbidden")}
	if waited, err := WaitForBoot(ctx, f, "p0", 100, time.Second); err != nil || waited || f.reads != 1 {
		t.Fatalf("unreadable: waited=%v err=%v reads=%d — must not block", waited, err, f.reads)
	}
	f = &nologinFake{gone: 1 << 30}
	if _, err := WaitForBoot(ctx, f, "p0", 100, 20*time.Millisecond, WithPollInterval(5*time.Millisecond)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("never finishes: err=%v", err)
	}
}
