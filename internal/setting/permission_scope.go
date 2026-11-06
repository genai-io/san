package setting

import (
	"maps"
	"path/filepath"
)

// PermissionPaths returns a copy with native file paths resolved against the
// execution directory. It never changes the arguments passed to the tool.
func PermissionPaths(name string, args map[string]any, cwd string) map[string]any {
	switch name {
	case "Read", "Write", "Edit":
	default:
		return args
	}
	path, _ := args["file_path"].(string)
	if path == "" || filepath.IsAbs(path) || cwd == "" {
		return args
	}
	resolved := maps.Clone(args)
	resolved["file_path"] = filepath.Join(cwd, path)
	return resolved
}

// ScopedPermissions keeps rule matching on both the submitted and execution
// paths while safety checks see only the execution path.
type ScopedPermissions struct {
	Data *Data
	CWD  string
}

func (s ScopedPermissions) prepare(name string, args map[string]any) (*Data, map[string]any) {
	resolved := PermissionPaths(name, args, s.CWD)
	path, _ := args["file_path"].(string)
	resolvedPath, _ := resolved["file_path"].(string)
	if resolvedPath == path {
		return s.Data, resolved
	}
	data := *s.Data
	// Preserve original-path matches without changing deny/ask/allow precedence.
	extend := func(rules []string) []string {
		for _, pattern := range rules {
			if MatchesToolPattern(name, args, BuildRule(name, args), pattern) {
				return append(append([]string(nil), rules...), name+"(**)")
			}
		}
		return rules
	}
	data.Permissions.Deny = extend(data.Permissions.Deny)
	data.Permissions.Ask = extend(data.Permissions.Ask)
	data.Permissions.Allow = extend(data.Permissions.Allow)
	return &data, resolved
}
func (s ScopedPermissions) DenyRule(name string, args map[string]any) string {
	data, resolved := s.prepare(name, args)
	return data.DenyRule(name, resolved)
}
func (s ScopedPermissions) HasPermissionToUseTool(name string, args map[string]any, session *SessionPermissions) PermissionDecision {
	data, resolved := s.prepare(name, args)
	return data.HasPermissionToUseTool(name, resolved, session)
}
func (s ScopedPermissions) CanApproveByHook(name string, args map[string]any, session *SessionPermissions) bool {
	data, resolved := s.prepare(name, args)
	return data.CanApproveByHook(name, resolved, session)
}
