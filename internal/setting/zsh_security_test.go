package setting

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/tool/perm"
)

var zshReviewCommands = []string{
	`ls *(e:'echo ZSH_AUDIT >&2':)`,
	`ls *(.e:'echo ZSH_AUDIT >&2':)`,
	`ls *(+auditfn)`,
	`cat =(echo ZSH_AUDIT)`,
	`ls ${(e):-'$(echo ZSH_AUDIT)'}`,
	`ls ${HOME:e}`,
	`ls $~pattern`, `ls $=words`, `ls $^array`,
	"ls `echo ZSH_AUDIT`", `ls $(echo ZSH_AUDIT)`,
	`cat <(echo ZSH_AUDIT)`, `ls $((1+1))`, `ls $[1+1]`,
	`ls ${HOME[1]}`, `ls ${HOME:1}`, `ls ${HOME//x/y}`,
	`setopt SH_WORD_SPLIT`, `unsetopt NOMATCH`, `emulate sh`,
	`nice setopt SH_WORD_SPLIT`, `/usr/bin/setopt SH_WORD_SPLIT`,
	`set -o shwordsplit`, `set +o shwordsplit`, `set -eo shwordsplit`,
	`builtin setopt SH_WORD_SPLIT`, `builtin builtin setopt SH_WORD_SPLIT`,
	`noglob setopt SH_WORD_SPLIT`, `nocorrect setopt SH_WORD_SPLIT`,
	`exec setopt SH_WORD_SPLIT`, `true; - setopt SH_WORD_SPLIT`,
	`$CMD SH_WORD_SPLIT`, `"${CMD}" SH_WORD_SPLIT`,
	`set\opt SH_WORD_SPLIT`, `$'set\x6fpt' SH_WORD_SPLIT`,
	`set $flags shwordsplit`, `set -y`, `set -G`, `set -I`,
	`exec emulate sh`, `exec -- setopt SH_WORD_SPLIT`, `exec $CMD`, `exec set\opt SH_WORD_SPLIT`,
}

var zshPortableCommands = []string{
	`ls *.go`, `rg pattern .`, `git status`, `cd repo && git status`, `(ls)`,
	`ls $HOME`, `ls ${HOME}`, `ls "$HOME"`,
	`printf '$~literal $=literal $^literal $HOME'`, `printf "\$~literal \$HOME"`,
	`printf 'Continue? [y/N] '; read -r a; echo "answer=$a"`,
	`set -e`, `set -eu`, `set +eux`, `set -- -o positional`, `set -- "$HOME"`, `command -v ls`,
	`exec sleep 60`, `exec -- sleep 60`,
}

func TestZshReviewReason(t *testing.T) {
	for _, cmd := range zshReviewCommands {
		if zshReviewReason(cmd, proc.ShellZsh) == "" {
			t.Errorf("zsh command accepted: %s", cmd)
		}
		if zshReviewReason(cmd, proc.ShellBash) != "" {
			t.Errorf("Bash behavior changed: %s", cmd)
		}
	}
	for _, cmd := range zshPortableCommands {
		if reason := zshReviewReason(cmd, proc.ShellZsh); reason != "" {
			t.Errorf("portable command %q: %s", cmd, reason)
		}
	}
}

// A fresh process selects SAN_SHELL before proc's singleton is first read.
func TestNativeZshPermissionPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native zsh is supported on Unix")
	}
	if os.Getenv("SAN_ZSH_POLICY_TEST_CHILD") != "1" {
		if _, err := exec.LookPath("zsh"); err != nil {
			t.Skip("native zsh unavailable")
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestNativeZshPermissionPolicy$", "-test.v")
		for _, env := range os.Environ() {
			if !strings.HasPrefix(env, "SAN_SHELL=") && !strings.HasPrefix(env, "SAN_ZSH_POLICY_TEST_CHILD=") {
				cmd.Env = append(cmd.Env, env)
			}
		}
		cmd.Env = append(cmd.Env, "SAN_SHELL=zsh", "SAN_ZSH_POLICY_TEST_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native zsh policy: %v\n%s", err, out)
		}
		return
	}
	shell, err := proc.DefaultShell()
	if err != nil || shell.Kind != proc.ShellZsh {
		t.Fatalf("selected shell = %v, %v", shell, err)
	}
	for _, command := range zshReviewCommands {
		t.Run(command, func(t *testing.T) {
			args := map[string]any{"command": command}
			settings := &Data{Permissions: PermissionSettings{Allow: []string{"Bash"}}}
			session := &SessionPermissions{Mode: ModeAutoPilot, AllowedTools: map[string]bool{"Bash": true}}
			d := settings.HasPermissionToUseTool("Bash", args, session)
			if d.Behavior != perm.Prompt || d.Judgeable {
				t.Fatalf("automatic grant: %+v", d)
			}
			if IsReadOnlyBashCommand(command) || settings.CanApproveByHook("Bash", args, session) || UnrecoverableReason("Bash", args) == "" {
				t.Fatal("read-only, hook or judge guard missing")
			}
			if _, ok := MatchAllowList("Bash", args, []string{"Bash(ls:*)", "Bash(cat:*)"}); ok {
				t.Fatal("narrow allow accepted native expansion")
			}
			if rules := ExactAllowRules("Bash", args); rules != nil {
				t.Fatalf("invented exact rules: %v", rules)
			}
			session.Mode = ModeBypassPermissions
			if got := settings.HasPermissionToUseTool("Bash", args, session); got.Behavior != perm.Permit {
				t.Fatalf("bypass no longer intentional: %+v", got)
			}
		})
	}
	for _, cmd := range zshPortableCommands {
		args := map[string]any{"command": cmd}
		settings := &Data{Permissions: PermissionSettings{Allow: []string{"Bash"}}}
		if reason := UnrecoverableReason("Bash", args); reason != "" {
			t.Errorf("portable command %q needs human confirmation: %s", cmd, reason)
		}
		if got := settings.HasPermissionToUseTool("Bash", args, &SessionPermissions{Mode: ModeAutoPilot}); got.Behavior != perm.Permit {
			t.Errorf("portable command %q lost explicit grant: %+v", cmd, got)
		}
	}
	for _, cmd := range []string{`ls *.go`, `rg pattern .`, `git status`, `cd repo && git status`, `(ls)`, `ls $HOME`, `ls ${HOME}`} {
		if !IsReadOnlyBashCommand(cmd) {
			t.Errorf("portable command no longer read-only: %s", cmd)
		}
	}
}
