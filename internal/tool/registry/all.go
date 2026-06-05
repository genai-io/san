// Package registry imports all tool sub-packages to trigger their init() registration.
package registry

import (
	_ "github.com/genai-io/san/v2/internal/tool/agent"
	_ "github.com/genai-io/san/v2/internal/tool/cron"
	_ "github.com/genai-io/san/v2/internal/tool/fs"
	_ "github.com/genai-io/san/v2/internal/tool/mode"
	_ "github.com/genai-io/san/v2/internal/tool/skill"
	_ "github.com/genai-io/san/v2/internal/tool/task"
	_ "github.com/genai-io/san/v2/internal/tool/tasktools"
	_ "github.com/genai-io/san/v2/internal/tool/web"
)
