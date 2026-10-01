package proc

import (
	"os"
	"testing"
)

func TestStartTimeKnowsThisProcessAndNotADeadOne(t *testing.T) {
	a, ok := StartTime(os.Getpid())
	if !ok {
		t.Fatal("this process reads as not running")
	}
	if b, _ := StartTime(os.Getpid()); a != b {
		t.Errorf("start time changed between calls: %q vs %q", a, b)
	}
	if _, ok := StartTime(1 << 30); ok {
		t.Error("a pid no process holds reads as running")
	}
}
