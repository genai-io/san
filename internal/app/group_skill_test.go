package app

import (
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"

	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/tool"
)

func TestGroupSkillQueuedUntilDeliveryBoundary(t *testing.T) {
	m, provider := compactQueueModel(t)
	m.services.Skill = skill.NewRegistry()
	m.services.Tool = tool.NewRegistry()
	m.services.MCP = mcp.NewManagerForTest(nil)
	receipt := &core.TurnFinalization{}
	m.pendingMessages.Hold(receipt)
	m.userInput.Skill.SetPending("group", "Use Group to join the team")
	m.HandleSkillInvocation()
	if m.groupSkillTurn || m.pendingMessages.Len() != 1 || !m.pendingMessages.First().GroupSkill {
		t.Fatal("queued invocation changed active tools or lost invocation metadata")
	}
	m.pendingMessages.Finalize(receipt)
	m.sendPendingMessages()
	if !m.groupSkillTurn || m.disabledTools()[tool.ToolGroup] || m.pendingMessages.Len() != 0 {
		t.Fatal("delivery did not enable Group for the invocation")
	}
	select {
	case <-provider.requests:
	case <-time.After(3 * time.Second):
		t.Fatal("group invocation was not delivered")
	}

	// Harness input must wait for a separate turn with ordinary tools.
	m.sendToAgent(pendingMessage{Message: core.UserMessage("harness continuation", nil)})
	if !m.groupSkillTurn || m.pendingMessages.Len() != 1 {
		t.Fatal("harness input changed or entered the active group turn")
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-m.AgentOutbox():
			if _, ok := event.(core.TurnEnded); ok {
				conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: m.AgentOutbox(), Event: event})
				if m.groupSkillTurn || !m.disabledTools()[tool.ToolGroup] || m.pendingMessages.Len() != 0 {
					t.Fatal("ordinary successor retained Group tools")
				}
				return
			}
		case <-deadline:
			t.Fatal("group turn did not finish")
		}
	}
}

func TestQueuedGroupCommandWaitsForTurnEnd(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.conv.Stream.Active = true
	m.userInput.Queue.Enqueue("/group join shop", nil)
	if _, released := m.releaseQueuedMessage(); released || m.userInput.Queue.Len() != 1 || m.groupSkillTurn {
		t.Fatal("queued group command was injected into an ordinary turn")
	}
}

func TestGroupToolRequiresSkillTurn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group.BindSession(func() string { return "skill-test" })
	m := &model{services: services{Setting: setting.New(setting.NewData())}}
	if !m.disabledTools()[tool.ToolGroup] {
		t.Fatal("Group exposed without invocation")
	}
	if _, err := group.Join("shop", group.Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = group.Leave() })
	if !m.disabledTools()[tool.ToolGroup] {
		t.Fatal("membership exposed Group")
	}
	if m.disabledTools()[tool.ToolSendMessage] {
		t.Fatal("membership must still enable messaging")
	}
	before := m.disabledToolsSignature()
	m.groupSkillTurn = true
	if m.disabledTools()[tool.ToolGroup] {
		t.Fatal("Group hidden during explicit skill invocation")
	}
	if before == m.disabledToolsSignature() {
		t.Fatal("skill invocation must rebuild toolset")
	}
	m.groupSkillTurn = false
	if !m.disabledTools()[tool.ToolGroup] {
		t.Fatal("Group remained exposed after skill turn")
	}
}
