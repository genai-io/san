package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHandleNameCommand_Rename(t *testing.T) {
	var got string
	c := NewSlashCommandController(SlashCommandEnv{
		RenameSession: func(name string) error { got = name; return nil },
	})
	out, _, err := c.handleNameCommand(context.Background(), "  My Awesome Session  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "My Awesome Session" {
		t.Errorf("RenameSession called with %q, want trimmed %q", got, "My Awesome Session")
	}
	if !strings.Contains(out, "My Awesome Session") {
		t.Errorf("output = %q, want it to include the session name", out)
	}
}

func TestHandleNameCommand_EmptyArgsShowsUsage(t *testing.T) {
	c := NewSlashCommandController(SlashCommandEnv{})
	out, _, err := c.handleNameCommand(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("empty args should show usage, got = %q", out)
	}
}

func TestHandleNameCommand_WhitespaceOnlyShowsUsage(t *testing.T) {
	c := NewSlashCommandController(SlashCommandEnv{})
	out, _, err := c.handleNameCommand(context.Background(), "   ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("whitespace-only args should show usage, got = %q", out)
	}
}

func TestHandleNameCommand_FailureSurfaced(t *testing.T) {
	c := NewSlashCommandController(SlashCommandEnv{
		RenameSession: func(string) error { return errors.New("boom") },
	})
	out, _, err := c.handleNameCommand(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want boom", err)
	}
	if out != "" {
		t.Errorf("output = %q, want empty on error", out)
	}
}

func TestHandleNameCommand_Unavailable(t *testing.T) {
	c := NewSlashCommandController(SlashCommandEnv{}) // no RenameSession wired
	out, _, err := c.handleNameCommand(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not available") {
		t.Errorf("output = %q, want an unavailable message", out)
	}
}
