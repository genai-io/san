package setting

// dangerousPrefixes are bash command prefixes that should never be suggested
// as allow rules because they enable arbitrary code execution.
var dangerousPrefixes = map[string]bool{
	"bash":   true,
	"sh":     true,
	"zsh":    true,
	"fish":   true,
	"eval":   true,
	"source": true,
	".":      true,
	"exec":   true,
	"sudo":   true,
	"env":    true,
	"xargs":  true,
	"ssh":    true,
	"python": true, "python3": true, "python2": true,
	"node": true,
	"deno": true,
	"ruby": true,
	"perl": true,
	"php":  true,
	"lua":  true,
	"npx":  true,
	"bunx": true,
}

// MaxSuggestedRules is the maximum number of suggested rules for compound commands.
const MaxSuggestedRules = 5
