package setting

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// destructiveCommands are patterns that should always require user confirmation,
// even when session permissions like AllowAllBash are enabled.
// These commands can cause irreversible data loss or system damage.
var destructiveCommands = []string{
	"rm:-rf",
	"rm:-fr",
	"rm:-r",
	"git:reset --hard",
	"git:clean -fd",
	"git:clean -f",
	"git:push -f",
	"git:checkout --",
	"git:stash drop",
	"git:stash clear",
	"git:branch -D",
	"git:branch -d -f",
	"chmod:777",
	"chmod:-R 777",
	":(){ :|:& };:", // fork bomb
	"> /dev/",       // device writes
	"dd:if=",        // direct disk access
	"mkfs",          // filesystem creation
	"fdisk",         // disk partitioning
}

// isDestructiveCommand checks if a bash command matches any destructive pattern.
// Returns true if the command should always require user confirmation.
func isDestructiveCommand(cmd string) bool {
	for _, normalized := range normalizedBashCommands(cmd) {
		for _, pattern := range destructiveCommands {
			if strings.Contains(normalized, pattern) {
				return true
			}
		}
		if isGitPushForce(normalized) {
			return true
		}
	}
	return false
}

// isGitPushForce detects "git push --force" without false-positiving on
// "--force-with-lease" or "--force-if-includes".
func isGitPushForce(normalized string) bool {
	if !strings.HasPrefix(normalized, "git:push ") {
		return false
	}
	args := strings.Fields(normalized[len("git:push "):])
	for _, arg := range args {
		if arg == "--force" {
			return true
		}
	}
	return false
}

// CommonAllowPatterns contains commonly allowed patterns.
var CommonAllowPatterns = []string{
	"Bash(git:*)",
	"Bash(npm:*)",
	"Bash(yarn:*)",
	"Bash(pnpm:*)",
	"Bash(go:*)",
	"Bash(make:*)",
	"Bash(ls:*)",
	"Bash(cat:*)",
	"Bash(head:*)",
	"Bash(tail:*)",
	"Bash(pwd)",
}

// ---------------------------------------------------------------------------
// Bypass-immune path safety checks
// Inspired by Claude Code's checkPathSafetyForAutoEdit — these checks cannot
// be bypassed by session permissions or allow rules.
// ---------------------------------------------------------------------------

// sensitiveDirectories are directory names that should always require
// confirmation when editing files within them. They contain configuration or
// metadata that, if tampered with, can execute code or break tooling.
var sensitiveDirectories = []string{
	".git",    // Git hooks can execute arbitrary code
	".claude", // Claude Code configuration
	".san",    // San configuration
	".gen",    // San configuration (pre-rename, kept for back-compat)
	".vscode", // VS Code extensions, launch configs
	".idea",   // JetBrains IDE configs
	".ssh",    // SSH keys and config
	".aws",    // AWS credentials
	".gnupg",  // GPG keys
	".kube",   // Kubernetes configs
}

// sensitiveFiles are specific filenames (basenames) that should always require
// confirmation because they can execute code on shell startup or contain
// credentials.
var sensitiveFiles = map[string]string{
	".bashrc":             "shell startup script",
	".bash_profile":       "shell startup script",
	".zshrc":              "shell startup script",
	".zprofile":           "shell startup script",
	".profile":            "shell startup script",
	".zshenv":             "shell startup script",
	".login":              "shell startup script",
	".gitconfig":          "git configuration (hooks, aliases)",
	".gitmodules":         "git submodule config",
	".npmrc":              "npm config (may contain auth tokens)",
	".pypirc":             "PyPI config (may contain auth tokens)",
	".netrc":              "network credentials",
	".docker/config.json": "Docker credentials",
}

// isSensitivePath checks if a file path points to a sensitive location that
// should always require user confirmation (bypass-immune).
// Returns a human-readable reason if sensitive, or empty string if safe.
func isSensitivePath(filePath string) string {
	// Resolve symlinks to prevent bypass via symlink chains
	resolved, err := filepath.EvalSymlinks(filepath.Dir(filePath))
	if err == nil {
		filePath = filepath.Join(resolved, filepath.Base(filePath))
	}

	// Normalize to absolute path
	if !filepath.IsAbs(filePath) {
		if abs, err := filepath.Abs(filePath); err == nil {
			filePath = abs
		}
	}

	// Check each path component for sensitive directories
	parts := strings.Split(filePath, string(os.PathSeparator))
	for _, part := range parts {
		for _, dir := range sensitiveDirectories {
			if part == dir {
				return dir + "/ directory"
			}
		}
	}

	// Check basename against sensitive files
	basename := filepath.Base(filePath)
	if reason, ok := sensitiveFiles[basename]; ok {
		return basename + " (" + reason + ")"
	}

	// Check two-level paths like ".docker/config.json"
	if len(parts) >= 2 {
		twoLevel := parts[len(parts)-2] + "/" + basename
		if reason, ok := sensitiveFiles[twoLevel]; ok {
			return twoLevel + " (" + reason + ")"
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// Enhanced bash security checks
// Inspired by Claude Code's bashSecurity.ts — detects obfuscation, injection,
// and other shell security issues beyond simple destructive patterns.
// ---------------------------------------------------------------------------

// zshDangerousCommands are Zsh-specific builtins that can bypass restrictions
// or access system resources directly.
var zshDangerousCommands = []string{
	"zmodload", // Load kernel modules
	"emulate",  // Change shell emulation mode
	"sysopen",  // Direct file descriptor access
	"sysread",  // Direct system read
	"syswrite", // Direct system write
	"sysseek",  // Direct seek
	"zpty",     // Pseudo-terminal control
	"ztcp",     // Raw TCP connections
	"zsocket",  // Unix socket access
	"zf_rm",    // Bypass safe rm
	"zf_mv",    // Bypass safe mv
	"zf_ln",    // Bypass safe ln
	"zf_chmod", // Direct chmod
	"zf_chown", // Direct chown
}

// bashSecurityPatterns defines patterns that indicate potential shell injection
// or obfuscation attempts.
var bashSecurityPatterns = []struct {
	check  func(string) bool
	reason string
}{
	{hasCommandSubstitution, "command substitution detected"},
	{hasObfuscatedFlags, "obfuscated flags detected"},
	{hasControlCharacters, "control characters detected"},
	{hasIFSInjection, "IFS injection detected"},
	{hasZshDangerousCommand, "zsh dangerous command"},
	{hasProcEnvironAccess, "/proc/environ access"},
	{hasSuspiciousRedirection, "suspicious redirection"},
}

// checkBashSecurity performs security analysis on a bash command beyond simple
// destructive pattern matching. Returns a reason string if the command is
// suspicious, or empty string if it appears safe.
func checkBashSecurity(cmd string) string {
	// AST-based checks first (more accurate, structural analysis)
	if file := parseBashAST(cmd); file != nil {
		if reason := checkASTSecurity(file); reason != "" {
			return reason
		}
	}

	// Regex-based checks as fallback / catch-all
	for _, p := range bashSecurityPatterns {
		if p.check(cmd) {
			return p.reason
		}
	}
	return ""
}

func hasCommandSubstitution(cmd string) bool {
	// Detect $() and backtick substitution in dangerous contexts
	// Allow simple $(cmd) but flag nested/complex patterns
	depth := 0
	for i := 0; i < len(cmd)-1; i++ {
		if cmd[i] == '$' && cmd[i+1] == '(' {
			depth++
			if depth > 1 {
				return true // Nested command substitution
			}
		}
		if cmd[i] == ')' && depth > 0 {
			depth--
		}
	}
	// Backtick substitution inside variable assignments
	if strings.Contains(cmd, "eval ") && (strings.Contains(cmd, "$(") || strings.Contains(cmd, "`")) {
		return true
	}
	return false
}

func hasObfuscatedFlags(cmd string) bool {
	// Detect backslash-escaped whitespace between flag characters
	// e.g., "r\m -r\f" to bypass pattern matching
	for i := 0; i < len(cmd)-1; i++ {
		if cmd[i] == '\\' {
			next := cmd[i+1]
			// Backslash followed by a letter mid-word (obfuscation attempt)
			if next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z' {
				// Check if this is within a flag-like context (after -)
				before := strings.TrimRight(cmd[:i], " \t")
				if len(before) > 0 && before[len(before)-1] == '-' {
					return true
				}
			}
		}
	}
	return false
}

func hasControlCharacters(cmd string) bool {
	for _, r := range cmd {
		// ASCII control chars except common ones (tab, newline, carriage return)
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
		// Unicode zero-width characters used for obfuscation
		if r == 0x200B || r == 0x200C || r == 0x200D || r == 0xFEFF {
			return true
		}
	}
	return false
}

func hasIFSInjection(cmd string) bool {
	return strings.Contains(cmd, "IFS=") || strings.Contains(cmd, "IFS =")
}

func hasZshDangerousCommand(cmd string) bool {
	// Split on &&, ;, then split each result on | for pipe segments
	segments := extractBashCommands(cmd)
	var expanded []string
	for _, seg := range segments {
		for _, pipePart := range strings.Split(seg, "|") {
			if s := strings.TrimSpace(pipePart); s != "" {
				expanded = append(expanded, s)
			}
		}
	}

	for _, c := range expanded {
		parts := strings.Fields(c)
		if len(parts) == 0 {
			continue
		}
		if slices.Contains(zshDangerousCommands, filepath.Base(parts[0])) {
			return true
		}
	}
	return false
}

func hasProcEnvironAccess(cmd string) bool {
	return strings.Contains(cmd, "/proc/") && strings.Contains(cmd, "environ")
}

func hasSuspiciousRedirection(cmd string) bool {
	// Detect output redirection to sensitive system paths
	suspiciousPaths := []string{
		"> /etc/", ">> /etc/",
		"> /dev/sd", ">> /dev/sd",
		"> /dev/nvme", ">> /dev/nvme",
		"> ~/.ssh/", ">> ~/.ssh/",
		"> ~/.bashrc", ">> ~/.bashrc",
		"> ~/.zshrc", ">> ~/.zshrc",
		"> ~/.profile", ">> ~/.profile",
	}
	lower := strings.ToLower(cmd)
	for _, p := range suspiciousPaths {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Denial tracking — prevents infinite denial loops and surfaces potential
// classifier or rule misconfiguration.
// ---------------------------------------------------------------------------

// denialLimits configures when the system falls back to prompting the user
// instead of auto-denying.
var denialLimits = struct {
	MaxConsecutive int // Fall back to prompting after N consecutive denials
	MaxTotal       int // Fall back to prompting after N total denials in session
}{
	MaxConsecutive: 3,
	MaxTotal:       20,
}

// DenialTracking tracks permission denials during a session.
type DenialTracking struct {
	ConsecutiveDenials int
	TotalDenials       int
}

// RecordDenial records a denial and returns true if the system should fall
// back to prompting the user.
func (d *DenialTracking) RecordDenial() bool {
	d.ConsecutiveDenials++
	d.TotalDenials++
	return d.ShouldFallbackToPrompting()
}

// RecordSuccess resets the consecutive denial counter.
func (d *DenialTracking) RecordSuccess() {
	d.ConsecutiveDenials = 0
}

// ShouldFallbackToPrompting returns true if denial limits are exceeded.
func (d *DenialTracking) ShouldFallbackToPrompting() bool {
	return d.ConsecutiveDenials >= denialLimits.MaxConsecutive ||
		d.TotalDenials >= denialLimits.MaxTotal
}
