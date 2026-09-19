// Package register imports every tool sub-package for its init() side effect,
// so a blank import of this package registers all built-in tools.
package register

import (
	_ "github.com/genai-io/san/internal/tool/agent"
	_ "github.com/genai-io/san/internal/tool/ask"
	_ "github.com/genai-io/san/internal/tool/cron"
	_ "github.com/genai-io/san/internal/tool/evolve"
	_ "github.com/genai-io/san/internal/tool/fs"
	_ "github.com/genai-io/san/internal/tool/skill"
	_ "github.com/genai-io/san/internal/tool/todo"
	_ "github.com/genai-io/san/internal/tool/web"
)
