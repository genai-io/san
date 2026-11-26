package input

import (
	"strings"
	"testing"

	coremcp "github.com/genai-io/san/internal/mcp"
)

func TestMCPErrorPreviewUsesDisplayColumns(t *testing.T) {
	s := NewMCPSelector(nil)
	s.active, s.width, s.height = true, 100, 32
	for _, diagnostic := range []string{"连接失败：请检查服务器地址", strings.Repeat("x", 40)} {
		item := mcpServerItem{Name: "demo", Status: coremcp.StatusError, Error: diagnostic}
		s.servers = []mcpServerItem{item}
		s.filteredServers = s.servers
		s.level = mcpLevelList
		out := s.Render()
		if strings.HasPrefix(diagnostic, "x") {
			if strings.Contains(out, diagnostic) || !strings.Contains(out, strings.Repeat("x", 27)) {
				t.Fatalf("unbounded or missing preview: %s", out)
			}
		} else if !strings.Contains(out, diagnostic) {
			t.Fatalf("missing localized diagnostic: %s", out)
		}
		s.detailServer = &item
		s.level = mcpLevelDetail
		if !strings.Contains(s.Render(), diagnostic) {
			t.Fatal("detail lost full error")
		}
	}
}
