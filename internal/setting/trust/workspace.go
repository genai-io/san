package trust

import (
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
)

// Findings lists what cwd's own configuration would turn on
// once trusted: model routing, hooks, allow rules, MCP servers, plugins.
func Findings(cwd string) []string {
	findings := llm.ProjectModelFindings(cwd)
	findings = append(findings, setting.ProjectSettingsFindings(cwd)...)
	if setting.WorkingDirEnvNeedsTrust(cwd) {
		findings = append(findings, "load .env on the next launch from this directory")
	}
	findings = append(findings, mcp.ProjectServerFindings(cwd)...)
	return append(findings, plugin.ProjectPluginFindings(cwd)...)
}

// ReviewAndTrustIfClean returns what waits for cwd's trust, or nil when cwd is
// trusted. A folder with nothing to review is trusted for this run.
func ReviewAndTrustIfClean(cwd string) []string {
	if setting.IsWorkspaceTrusted(cwd) {
		return nil
	}
	findings := Findings(cwd)
	if len(findings) == 0 {
		setting.TrustWorkspaceForSession(cwd)
	}
	return findings
}
