package setting

import (
	"strings"

	"github.com/genai-io/san/internal/proc"
	"mvdan.cc/sh/v3/syntax"
)

// nativeZshReviewReason fences automatic grants made by the Bash parser. It is
// deliberately not a zsh parser: native expansions need human review, even when
// mvdan accepts their text as Bash (notably code-bearing glob qualifiers).
func nativeZshReviewReason(cmd string) string {
	shell, err := proc.DefaultShell()
	if err != nil {
		return ""
	}
	return zshReviewReason(cmd, shell.Kind)
}

func zshReviewReason(cmd string, kind proc.ShellKind) string {
	if kind != proc.ShellZsh {
		return ""
	}
	const reason = "native zsh syntax requires human confirmation"
	file := parseBashAST(cmd)
	if file == nil {
		return reason
	}
	unsafe := false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				break
			}
			head, literal := literalWord(n.Args[0])
			if !literal {
				unsafe = true
				break
			}
			// Native precommands and option mutators need review, including
			// when they occur as exec targets or normalized command names.
			if isZshReviewCommand(head) {
				unsafe = true
				break
			}
			switch head {
			case "exec":
				words := n.Args[1:]
				if len(words) > 0 {
					if first, ok := literalWord(words[0]); ok && first == "--" {
						words = words[1:]
					}
				}
				if len(words) > 0 {
					target, ok := literalWord(words[0])
					if !ok || strings.HasPrefix(target, "-") || target == "set" || target == "exec" || isZshReviewCommand(target) {
						unsafe = true
					}
				}
			case "set":
				for _, word := range n.Args[1:] {
					arg, literal := literalWord(word)
					if !literal {
						unsafe = true
						break
					}
					if len(arg) < 2 || arg == "--" || (arg[0] != '-' && arg[0] != '+') {
						break
					}
					for _, flag := range arg[1:] {
						if !strings.ContainsRune("eufvx", flag) {
							unsafe = true
						}
					}
				}
			}
		case *syntax.SglQuoted:
			return false // Literal dollars and backticks cannot expand here.
		case *syntax.ParamExp:
			// Only the exact portable $NAME and ${NAME} spellings are safe.
			text := cmd[n.Pos().Offset():n.End().Offset()]
			name := n.Param.Value
			if !portableParameterName(name) || (text != "$"+name && text != "${"+name+"}") {
				unsafe = true
			}
			return false
		case *syntax.ExtGlob, *syntax.CmdSubst, *syntax.ProcSubst,
			*syntax.ArithmExp, *syntax.ArithmCmd, *syntax.LetClause:
			unsafe = true
		case *syntax.Lit:
			// Bash lexes zsh's unbraced $~, $= and $^ expansions as
			// literal text. Inspect source to preserve escaped dollars.
			text := cmd[n.Pos().Offset():n.End().Offset()]
			for i := 0; i < len(text); i++ {
				if text[i] == '\\' {
					i++
					continue
				}
				at := int(n.Pos().Offset()) + i
				if text[i] == '$' && at+1 < len(cmd) && strings.ContainsRune("~=^", rune(cmd[at+1])) {
					unsafe = true
				}
			}
		}
		return !unsafe
	})
	if unsafe {
		return reason
	}
	// Keep the shared extractor's path stripping and wrapper normalization.
	for _, command := range extractCommandsAST(file) {
		if isZshReviewCommand(command.Name) {
			return reason
		}
	}
	return ""
}

func isZshReviewCommand(head string) bool {
	switch head {
	case "setopt", "unsetopt", "emulate", "builtin", "noglob", "nocorrect", "-":
		return true
	}
	return false
}

func portableParameterName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}
