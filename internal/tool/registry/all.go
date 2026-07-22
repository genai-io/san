// Package registry imports all tool sub-packages to trigger their init() registration.
package registry

import (
	_ "github.com/genai-io/san/internal/tool/agent"
	_ "github.com/genai-io/san/internal/tool/cron"
	_ "github.com/genai-io/san/internal/tool/evolve"
	_ "github.com/genai-io/san/internal/tool/fs"
	_ "github.com/genai-io/san/internal/tool/mode"
	_ "github.com/genai-io/san/internal/tool/skill"
	_ "github.com/genai-io/san/internal/tool/tasktools"
	_ "github.com/genai-io/san/internal/tool/web"
)
