package setting

import (
	"testing"
)

func Test_parseBashAST(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		wantNil bool
	}{
		{"simple command", "ls -la", false},
		{"chained", "cd /tmp && git pull", false},
		{"pipe", "cat file | grep pattern", false},
		{"subshell", "echo $(whoami)", false},
		{"heredoc", "cat <<EOF\nhello\nEOF", false},
		// Malformed input should return nil (graceful fallback)
		{"unterminated quote", "echo 'hello", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBashAST(tt.cmd)
			if (got == nil) != tt.wantNil {
				t.Errorf("parseBashAST(%q) nil=%v, wantNil=%v", tt.cmd, got == nil, tt.wantNil)
			}
		})
	}
}

func Test_extractCommandsAST(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		wantCmds []string // expected command names
	}{
		{"simple", "ls -la", []string{"ls"}},
		{"chained and", "cd /tmp && git pull", []string{"cd", "git"}},
		{"chained semi", "echo hello; echo world", []string{"echo", "echo"}},
		{"chained or", "test -f file || echo missing", []string{"test", "echo"}},
		{"pipe", "cat file | grep pattern | wc -l", []string{"cat", "grep", "wc"}},
		{"env var wrapper", "NODE_ENV=prod npm start", []string{"npm"}},
		{"timeout wrapper", "timeout 30 make test", []string{"make"}},
		{"nice wrapper", "nice -n 10 gcc main.c", []string{"gcc"}},
		{"stdbuf wrapper", "stdbuf -oL npm test", []string{"npm"}},
		{"ionice not stripped", "ionice -c2 make test", []string{"ionice"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := parseBashAST(tt.cmd)
			if file == nil {
				t.Fatalf("parseBashAST(%q) returned nil", tt.cmd)
			}
			commands := extractCommandsAST(file)
			if len(commands) != len(tt.wantCmds) {
				names := make([]string, len(commands))
				for i, c := range commands {
					names[i] = c.Name
				}
				t.Fatalf("got %d commands %v, want %d %v", len(commands), names, len(tt.wantCmds), tt.wantCmds)
			}
			for i, want := range tt.wantCmds {
				if commands[i].Name != want {
					t.Errorf("command[%d].Name = %q, want %q", i, commands[i].Name, want)
				}
			}
		})
	}
}

func Test_extractCommandsAST_Pipe(t *testing.T) {
	file := parseBashAST("cat file | grep pattern")
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	for _, cmd := range commands {
		if !cmd.HasPipe {
			t.Errorf("command %q should have HasPipe=true", cmd.Name)
		}
	}
}

func Test_extractCommandsAST_Redirect(t *testing.T) {
	file := parseBashAST("echo data > /tmp/output.txt")
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	if len(commands) != 1 {
		t.Fatalf("got %d commands, want 1", len(commands))
	}
	if len(commands[0].RedirPaths) != 1 || commands[0].RedirPaths[0] != "/tmp/output.txt" {
		t.Errorf("RedirPaths = %v, want [/tmp/output.txt]", commands[0].RedirPaths)
	}
}

func Test_extractCommandsAST_PathStripping(t *testing.T) {
	file := parseBashAST("/usr/bin/git status")
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	if len(commands) != 1 || commands[0].Name != "git" {
		t.Errorf("expected 'git', got %q", commands[0].Name)
	}
}

func Test_checkASTSecurity(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		wantSafe bool
	}{
		// Safe commands
		{"simple ls", "ls -la", true},
		{"git status", "git status", true},
		{"npm install", "npm install lodash", true},
		{"pipe", "cat file | grep pattern", true},
		{"chained safe", "cd /tmp && ls", true},
		{"cd git status", "cd /tmp/repo && git status", true},
		{"cd git describe", "cd /tmp/repo && git describe --tags --abbrev=0", true},
		{"cd git tag list", "cd /tmp/repo && git tag --sort=-v:refname", true},

		// Dangerous: builtins
		{"eval", "eval 'rm -rf /'", false},
		{"source", "source ~/.bashrc", false},
		{"dot source", ". /tmp/evil.sh", false},

		// Dangerous: cd + git
		{"cd git compound", "cd /tmp/repo && git pull", false},
		{"cd git separated", "cd /tmp; git clone url", false},
		{"cd git tag create", "cd /tmp/repo && git tag v1.2.3", false},
		{"cd git branch create", "cd /tmp/repo && git branch release", false},
		{"cd git remote add", "cd /tmp/repo && git remote add origin git@github.com:genai-io/san.git", false},

		// Dangerous: redirect to sensitive path
		{"redirect to etc", "echo bad > /etc/passwd", false},
		{"redirect to ssh", "echo key >> ~/.ssh/authorized_keys", false},

		// Dangerous: nested command substitution
		{"nested subst", "echo $(echo $(whoami))", false},

		// Safe: single-level command substitution
		{"single subst", "echo $(date)", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := parseBashAST(tt.cmd)
			if file == nil {
				t.Skipf("parseBashAST(%q) returned nil", tt.cmd)
			}
			reason := checkASTSecurity(file)
			isSafe := reason == ""
			if isSafe != tt.wantSafe {
				t.Errorf("checkASTSecurity(%q) = %q, wantSafe=%v", tt.cmd, reason, tt.wantSafe)
			}
		})
	}
}

func Test_extractCommandsAST_CaseClause(t *testing.T) {
	file := parseBashAST(`case "$1" in start) systemctl start nginx;; stop) rm -rf /tmp/cache;; esac`)
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.Name
	}
	if len(commands) != 2 {
		t.Fatalf("got %d commands %v, want 2 [systemctl, rm]", len(commands), names)
	}
	if commands[0].Name != "systemctl" {
		t.Errorf("command[0] = %q, want systemctl", commands[0].Name)
	}
	if commands[1].Name != "rm" {
		t.Errorf("command[1] = %q, want rm", commands[1].Name)
	}
}

func Test_extractCommandsAST_FuncDecl(t *testing.T) {
	file := parseBashAST(`cleanup() { rm -rf /tmp/data; }`)
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	if len(commands) != 1 || commands[0].Name != "rm" {
		names := make([]string, len(commands))
		for i, c := range commands {
			names[i] = c.Name
		}
		t.Errorf("got %v, want [rm]", names)
	}
}

func Test_extractCommandsAST_ElifChain(t *testing.T) {
	script := `if test -f a; then echo found; elif test -f b; then curl evil.com; else wget evil.com; fi`
	file := parseBashAST(script)
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.Name
	}
	// Should extract: test, echo, test, curl, wget
	want := []string{"test", "echo", "test", "curl", "wget"}
	if len(commands) != len(want) {
		t.Fatalf("got %d commands %v, want %d %v", len(commands), names, len(want), want)
	}
	for i, w := range want {
		if commands[i].Name != w {
			t.Errorf("command[%d] = %q, want %q", i, commands[i].Name, w)
		}
	}
}

func Test_extractCommandsAST_WhileCondition(t *testing.T) {
	file := parseBashAST(`while curl -s http://example.com; do sleep 1; done`)
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.Name
	}
	// Should extract both the condition (curl) and body (sleep)
	if len(commands) != 2 {
		t.Fatalf("got %d commands %v, want 2 [curl, sleep]", len(commands), names)
	}
	if commands[0].Name != "curl" {
		t.Errorf("command[0] = %q, want curl", commands[0].Name)
	}
	if commands[1].Name != "sleep" {
		t.Errorf("command[1] = %q, want sleep", commands[1].Name)
	}
}

func Test_checkASTSecurity_CaseClauseEval(t *testing.T) {
	// eval hidden inside a case clause should be caught
	file := parseBashAST(`case "$1" in run) eval "$cmd";; esac`)
	if file == nil {
		t.Skip("parse failed")
	}
	reason := checkASTSecurity(file)
	if reason == "" {
		t.Error("expected eval inside case clause to be flagged")
	}
}

func Test_checkASTSecurity_FuncDeclSource(t *testing.T) {
	// source hidden inside a function should be caught
	file := parseBashAST(`setup() { source /tmp/evil.sh; }`)
	if file == nil {
		t.Skip("parse failed")
	}
	reason := checkASTSecurity(file)
	if reason == "" {
		t.Error("expected source inside function to be flagged")
	}
}

func Test_checkASTSecurity_ElifEval(t *testing.T) {
	// eval hidden in elif branch should be caught
	file := parseBashAST(`if true; then echo ok; elif true; then eval "bad"; fi`)
	if file == nil {
		t.Skip("parse failed")
	}
	reason := checkASTSecurity(file)
	if reason == "" {
		t.Error("expected eval inside elif branch to be flagged")
	}
}

func Test_extractCommandsAST_CoprocClause(t *testing.T) {
	file := parseBashAST(`coproc myproc { curl http://evil.com; }`)
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	if len(commands) != 1 || commands[0].Name != "curl" {
		names := make([]string, len(commands))
		for i, c := range commands {
			names[i] = c.Name
		}
		t.Errorf("got %v, want [curl]", names)
	}
}

func Test_checkASTSecurity_CoprocEval(t *testing.T) {
	// eval hidden inside a coproc should be caught
	file := parseBashAST(`coproc { eval "$cmd"; }`)
	if file == nil {
		t.Skip("parse failed")
	}
	reason := checkASTSecurity(file)
	if reason == "" {
		t.Error("expected eval inside coproc to be flagged")
	}
}

func Test_extractCommandsAST_DeclClause(t *testing.T) {
	file := parseBashAST(`export PATH="/usr/bin"`)
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	if len(commands) != 1 || commands[0].Name != "export" {
		names := make([]string, len(commands))
		for i, c := range commands {
			names[i] = c.Name
		}
		t.Errorf("got %v, want [export]", names)
	}
}

func Test_extractCommandsAST_DeclareLocal(t *testing.T) {
	file := parseBashAST(`declare -a arr=(1 2 3); local x=5`)
	if file == nil {
		t.Fatal("parse failed")
	}
	commands := extractCommandsAST(file)
	if len(commands) != 2 {
		names := make([]string, len(commands))
		for i, c := range commands {
			names[i] = c.Name
		}
		t.Fatalf("got %d commands %v, want 2 [declare, local]", len(commands), names)
	}
	if commands[0].Name != "declare" {
		t.Errorf("command[0] = %q, want declare", commands[0].Name)
	}
	if commands[1].Name != "local" {
		t.Errorf("command[1] = %q, want local", commands[1].Name)
	}
}

func Test_checkASTSecurity_ExcessiveCommands(t *testing.T) {
	// Build a command with 51 subcommands
	parts := make([]string, 51)
	for i := range parts {
		parts[i] = "echo hi"
	}
	cmd := ""
	for i, p := range parts {
		if i > 0 {
			cmd += " && "
		}
		cmd += p
	}

	file := parseBashAST(cmd)
	if file == nil {
		t.Fatal("parse failed")
	}
	reason := checkASTSecurity(file)
	if reason == "" {
		t.Error("expected excessive command count to be flagged")
	}
}
