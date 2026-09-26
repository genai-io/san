package setting

import (
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

// Read-only runs without asking, so every way of hiding a second command, a
// write, or code execution inside an otherwise harmless command must fail it.
func TestIsReadOnlyPowerShellCommand(t *testing.T) {
	readOnly := []string{
		"Get-ChildItem -Recurse -Filter *.go",
		"gci src | Select-Object -First 5",
		"Get-Content README.md | Select-String TODO",
		"ls",
		"cat go.mod",
		"Get-ChildItem | Where-Object Length -gt 1000 | Sort-Object Length",
		"git status",
		"git log --oneline -5",
		"rg --type go TODO",
		"Test-Path .git",
	}
	notReadOnly := []string{
		"",
		"Remove-Item x",
		"Set-Content x y",
		"Get-ChildItem; Remove-Item x",
		"Get-ChildItem && Remove-Item x",
		"Get-ChildItem || Remove-Item x",
		"gci | Remove-Item",
		"Get-Content (Remove-Item x)",
		"Get-ChildItem $(Remove-Item x)",
		"Get-ChildItem | Where-Object { Remove-Item x }",
		"Get-ChildItem | ForEach-Object Delete",
		"Get-Content x > y",
		"Get-Content x 2>&1",
		"[IO.File]::Delete('x')",
		"Invoke-Expression 'Remove-Item x'",
		"iex 'x'",
		"& calc",
		". .\\build.ps1",
		"Get-ChildItem `\nRemove-Item x",
		"Get-ChildItem\nRemove-Item x",
		"Get-Content @args",
		"Get-Content \\\\attacker\\share\\x",
		"git push",
		"git status; git push",
		"rg --pre evil TODO",
		"curl https://example.com",
	}
	for _, cmd := range readOnly {
		if !IsReadOnlyPowerShellCommand(cmd) {
			t.Errorf("IsReadOnlyPowerShellCommand(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range notReadOnly {
		if IsReadOnlyPowerShellCommand(cmd) {
			t.Errorf("IsReadOnlyPowerShellCommand(%q) = true, want false", cmd)
		}
	}
}

func TestIsDestructivePowerShellCommand(t *testing.T) {
	destructive := []string{
		"Remove-Item -Recurse src",
		"Remove-Item src -Recurse -Force",
		"rm -r build",
		"rd /s /q build",
		"Get-ChildItem | Remove-Item -Recurse",
		"Write-Output x; Remove-Item -Rec x",
		"$(Remove-Item -Recurse x)",
		"cmd /c rd /s /q build",
		`pwsh -c "rm -r build"`,
		"Format-Volume -DriveLetter D",
		"Start-Process pwsh -Verb RunAs",
		"Register-ScheduledTask -TaskName x",
		"schtasks /create /tn x",
	}
	notDestructive := []string{
		"Remove-Item file.txt",
		"Get-ChildItem -Recurse",
		"git status",
	}
	for _, cmd := range destructive {
		if !isDestructivePowerShellCommand(cmd) {
			t.Errorf("isDestructivePowerShellCommand(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range notDestructive {
		if isDestructivePowerShellCommand(cmd) {
			t.Errorf("isDestructivePowerShellCommand(%q) = true, want false", cmd)
		}
	}
}

// The circuit breaker holds even in bypass mode, so it must see a drive root
// or the home directory however the removal is spelled or wrapped.
func TestPowerShellCircuitBreaker(t *testing.T) {
	trips := []string{
		`Remove-Item -Recurse -Force C:\`,
		`Remove-Item -Recurse C:\*`,
		`rm -r ~`,
		`Remove-Item -Recurse $env:USERPROFILE`,
		`Remove-Item -Recurse $HOME`,
		`rd /s /q D:`,
		`cmd /c rd /s /q C:\`,
		`pwsh -c "Remove-Item -Recurse C:\"`,
		`Get-ChildItem; Remove-Item -Recurse -Force \`,
		`rm -r /`,
	}
	holds := []string{
		`Remove-Item -Recurse C:\src\build`,
		`Remove-Item C:\`,
		`Get-ChildItem -Recurse C:\`,
	}
	for _, cmd := range trips {
		if CircuitBreakerReason("PowerShell", map[string]any{"command": cmd}) == "" {
			t.Errorf("circuit breaker did not trip on %q", cmd)
		}
	}
	for _, cmd := range holds {
		if reason := CircuitBreakerReason("PowerShell", map[string]any{"command": cmd}); reason != "" {
			t.Errorf("circuit breaker tripped on %q: %s", cmd, reason)
		}
	}
}

// An allow rule broader than the exact command covers a simple command only;
// deny and ask fire on any statement anywhere in the command.
func TestPowerShellRulePatterns(t *testing.T) {
	cases := []struct {
		pattern, cmd string
		allow, deny  bool
	}{
		{"PowerShell(git:status*)", "git status", true, true},
		{"PowerShell(git:status*)", "git status; Remove-Item -Recurse x", false, true},
		{"PowerShell(git:status*)", "git push", false, false},
		{"PowerShell(Remove-Item:*)", "gci | ForEach-Object { Remove-Item $_ }", false, true},
		{"PowerShell(Get-ChildItem *.go)", "Get-ChildItem *.go", true, true},
		{"PowerShell(Get-ChildItem *.go)", "Get-ChildItem x; Remove-Item y.go", false, false},
		{"PowerShell", "anything; at all", true, true},
		{"Bash", "anything", true, true},
		{"Bash(git status)", "git status", false, false},
	}
	for _, tc := range cases {
		args := map[string]any{"command": tc.cmd}
		if _, got := MatchAllowList("PowerShell", args, []string{tc.pattern}); got != tc.allow {
			t.Errorf("allow %q covers %q = %v, want %v", tc.pattern, tc.cmd, got, tc.allow)
		}
		if got := MatchesToolPattern("PowerShell", args, BuildRule("PowerShell", args), tc.pattern); got != tc.deny {
			t.Errorf("deny %q matches %q = %v, want %v", tc.pattern, tc.cmd, got, tc.deny)
		}
	}
}

func TestPowerShellReadOnlyRunsWithoutAsking(t *testing.T) {
	if d := ModeDefaultForCall("PowerShell", map[string]any{"command": "Get-ChildItem -Recurse"}, ModeNormal); d.Behavior != perm.Permit {
		t.Errorf("read-only PowerShell = %v, want permit", d.Behavior)
	}
	if d := ModeDefaultForCall("PowerShell", map[string]any{"command": "Set-Content x y"}, ModeNormal); d.Behavior == perm.Permit {
		t.Error("a writing PowerShell command ran without asking")
	}
	for _, cmd := range []string{"git reset --hard", "git.exe reset --hard"} {
		if reason, recoverable := ConfirmationTier("PowerShell", map[string]any{"command": cmd}); reason == "" || !recoverable {
			t.Errorf("%s under PowerShell = %q/%v, want the recoverable tier", cmd, reason, recoverable)
		}
	}
}

// A bare shell rule names the shell tool either way round, as a bare entry in
// an agent's allow_tools does.
func TestBareShellRuleCoversEitherShell(t *testing.T) {
	bash := map[string]any{"command": "ls"}
	if _, ok := MatchAllowList("Bash", bash, []string{"PowerShell"}); !ok {
		t.Error("allow: PowerShell did not cover a Bash call")
	}
	if !MatchesToolPattern("Bash", bash, BuildRule("Bash", bash), "PowerShell") {
		t.Error("deny: PowerShell did not match a Bash call")
	}
	if _, ok := MatchAllowList("Bash", bash, []string{"PowerShell(ls)"}); ok {
		t.Error("a PowerShell pattern was applied to a Bash call")
	}
}
