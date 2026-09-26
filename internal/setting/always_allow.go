package setting

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/tool/perm"
)

// "Always allow" persists the call the user just approved as allow rules. The
// rules are exact: never broader than what the approval dialog showed.

// codeRunners are commands that run whatever code they are handed, so even an
// exact rule for one grants more than the call approved. The shell interpreters,
// eval/source and Start-Process come from the safety tiers' own lists.
var codeRunners = map[string]bool{
	"exec": true, "sudo": true, "env": true, "xargs": true, "ssh": true,
	"python": true, "python3": true, "python2": true, "node": true, "deno": true, "ruby": true,
	"perl": true, "php": true, "lua": true, "npx": true, "bunx": true,
	// PowerShell's: they run other code or fetch it from the network.
	"pwsh": true, "powershell": true, "cmd": true,
	"invoke-expression": true, "iex": true, "invoke-command": true, "icm": true,
	"invoke-webrequest": true, "iwr": true, "invoke-restmethod": true, "irm": true, "curl": true, "wget": true,
}

func runsCode(name string) bool {
	return codeRunners[name] || shellInterpreters[name] || dangerousBuiltins[name] || powerShellStartProcess[name]
}

// ExactAllowRules returns the exact allow rules that cover this call, one per
// Bash subcommand, or nil when no rule can be exact: a redirect, substitution,
// env assignment, glob character, or a command that runs code.
func ExactAllowRules(toolName string, args map[string]any) []string {
	var rules []string
	switch toolName {
	case "Bash":
		cmd, _ := args["command"].(string)
		file := parseBashAST(cmd)
		if file == nil {
			return nil
		}
		for _, c := range extractCommandsAST(file) {
			if c.Name == "" || len(c.RedirPaths) > 0 || c.InSubshell || c.HasAssign || runsCode(c.Name) {
				return nil
			}
			if rule := "Bash(" + normalizeParsedCommand(c) + ")"; !slices.Contains(rules, rule) {
				rules = append(rules, rule)
			}
		}
	case "PowerShell":
		cmd, _ := args["command"].(string)
		if !isSimplePowerShell(cmd) || runsCode(commandName(strings.Fields(cmd)[0])) {
			return nil
		}
		fallthrough
	default:
		rules = []string{BuildRule(toolName, args)}
	}
	// A * or ? would make the saved rule a wildcard, broader than this call.
	if slices.ContainsFunc(rules, func(r string) bool { return strings.ContainsAny(r, "*?") }) {
		return nil
	}
	return rules
}

// AlwaysAllowRules returns the rules "Always allow" would save for this call,
// or nil when saving them would not stop it prompting — a deny, ask or safety
// check outranks allow rules, so offering the option there would do nothing.
func (s *Data) AlwaysAllowRules(toolName string, args map[string]any, session *SessionPermissions) []string {
	rules := ExactAllowRules(toolName, args)
	if len(rules) == 0 {
		return nil
	}
	with := *s // the gate only reads, so a shallow copy with its own allow list is enough
	with.Permissions.Allow = append(slices.Clip(s.Permissions.Allow), rules...)
	if with.CheckPermission(toolName, args, session) != perm.Permit {
		return nil
	}
	return rules
}

// addLocalAllowRules appends rules to permissions.allow in the project's
// .san/settings.local.json — the personal, highest-priority layer, so a grant
// is never committed for the whole team. Only that key is touched; a file
// that does not parse is left alone. Returns the file's path.
func addLocalAllowRules(cwd string, rules []string) (string, error) {
	path := filepath.Join(confdir.Dir(cwd), "settings.local.json")
	var doc map[string]any
	if err := atomicfile.ReadJSON(path, &doc); err != nil {
		return path, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok && doc["permissions"] != nil {
		return path, fmt.Errorf("%s: permissions is not an object", path)
	}
	if perms == nil {
		perms = map[string]any{}
	}
	allow, ok := perms["allow"].([]any)
	if !ok && perms["allow"] != nil {
		return path, fmt.Errorf("%s: permissions.allow is not a list", path)
	}
	for _, r := range rules {
		if !slices.Contains(allow, any(r)) {
			allow = append(allow, r)
		}
	}
	perms["allow"] = allow
	doc["permissions"] = perms
	if err := atomicfile.WriteJSON(path, doc, 0o644); err != nil {
		return path, err
	}
	invalidateLoaded()
	return path, nil
}
