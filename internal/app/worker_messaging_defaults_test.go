package app

import (
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

func TestOrdinaryMainRuntimeOffersWorkerMessaging(t *testing.T) {
	group.Release()
	for _, explicitlyDisabled := range []bool{false, true} {
		data := setting.NewData()
		if explicitlyDisabled {
			data.DisabledTools = map[string]bool{tool.ToolSendMessage: true}
		}
		m := &model{services: services{Setting: setting.New(data)}}
		params := agent.BuildParams{DisabledTools: m.disabledTools()}
		offered := false
		for _, schema := range params.ToolSchemas() {
			if schema.Name == tool.ToolSendMessage {
				offered = true
			}
		}
		if offered == explicitlyDisabled {
			t.Fatalf("ordinary main session SendMessage offered=%v, explicit disable=%v", offered, explicitlyDisabled)
		}
	}
}
