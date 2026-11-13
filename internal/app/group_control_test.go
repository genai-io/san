package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

func newGroupControlModel(t *testing.T) *model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	m := commitTestModel()
	m.env.CWD = t.TempDir()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.env.ApplyModePermissions(m.env.CWD)
	store, err := session.OpenStore(t.Context(), m.env.CWD)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner := mustCreateSession(t, store, session.CreateOptions{ID: "control-test", InitialCWD: m.env.CWD})
	m.services.SessionStore, m.services.Session, m.services.sessionID = store, owner, owner.ID()
	return m
}

func TestGroupControlQueueNeverEntersMainConversation(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m, provider := textOnlyModel(t)
		m.userInput.Queue.Enqueue("/group members", nil)
		m.userInput.Queue.Enqueue("/group members", nil)
		m.conv.Stream.Active = busy
		if _, released := m.releaseQueuedMessage(); !released || m.userInput.Queue.Len() != 0 {
			t.Fatal("local group commands stalled the queue")
		}
		for _, msg := range m.conv.Messages {
			if msg.Role == core.ChatUser {
				t.Fatal("group command entered main conversation")
			}
		}
		select {
		case <-provider.requests:
			t.Fatal("native group command invoked main provider")
		default:
		}
	}
}

func TestGroupControlStrictSchema(t *testing.T) {
	for _, text := range []string{
		`{"action":"members","arguments":{}}`,
		`{"action":"join","arguments":{"group":"team","name":"api","role":"API work","hold":true}}`,
		`{"action":"hold","arguments":{"hold":false}}`,
		`{"action":"kick","arguments":{"name":"api"}}`,
		`{"action":"disband","arguments":{"group":"team"}}`,
	} {
		if _, err := parseGroupControl(text); err != nil {
			t.Errorf("valid %s: %v", text, err)
		}
	}
	for _, text := range []string{
		`null`, `[]`, `{"action":"leave"}`, `{"action":"leave","arguments":null}`,
		`{"action":"leave","arguments":{},"extra":true}`,
		`{"action":"leave","arguments":{}} {}`,
		"```json\n{\"action\":\"leave\",\"arguments\":{}}\n```",
		`{"action":"leave","action":"members","arguments":{}}`,
		`{"action":"join","arguments":{"group":"a","group":"b"}}`,
		`{"action":"hold","arguments":{"hold":"true"}}`,
		`{"action":"hold","arguments":{"hold":null}}`,
		`{"action":"hold","arguments":{}}`,
		`{"action":"leave","arguments":{"group":"team"}}`,
		`{"action":"join","arguments":{"group":"../bad"}}`,
		`{"action":"join","arguments":{"group":"team","name":""}}`,
		`{"action":"join","arguments":{"group":"team","role":"line\nbreak"}}`,
		`{"action":"join","arguments":{}}`,
		`{"action":"kick","arguments":{}}`,
		`{"action":"execute","arguments":{}}`,
	} {
		if _, err := parseGroupControl(text); err == nil {
			t.Errorf("accepted invalid %s", text)
		}
	}
}

func TestGroupControlIsolatedCompletion(t *testing.T) {
	m := newGroupControlModel(t)
	p := &autopilotStubProvider{content: `{"action":"members","arguments":{}}`}
	m.env.LLMProvider = p
	m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatUser, Content: "PRIVATE TRANSCRIPT SENTINEL"})
	_, cmd := m.groupCommand("who is collaborating with me?")
	if cmd == nil {
		t.Fatal("no isolated completion")
	}
	msg := cmd().(groupControlMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	opts := p.lastOptions()
	if len(opts.Messages) != 1 {
		t.Fatalf("sent %d messages", len(opts.Messages))
	}
	text := opts.Messages[0].Content.Text()
	if strings.Contains(text+opts.SystemPrompt, "PRIVATE TRANSCRIPT SENTINEL") {
		t.Fatal("leaked transcript")
	}
	var input map[string]string
	if err := json.Unmarshal([]byte(text), &input); err != nil {
		t.Fatal(err)
	}
	if len(input) != 4 || input["request"] != "who is collaborating with me?" {
		t.Fatalf("unexpected context: %v", input)
	}
	if len(p.driver.Last().Tools) != 0 {
		t.Fatal("completion has tools")
	}
	if got := m.handleGroupControl(msg); got == "" {
		t.Fatal("result not applied")
	}
	if got := m.handleGroupControl(msg); got != "" {
		t.Fatal("result applied twice")
	}
}

func TestGroupControlNativeAndCancellation(t *testing.T) {
	m := newGroupControlModel(t)
	p := &autopilotStubProvider{content: `{"action":"members","arguments":{}}`}
	m.env.LLMProvider = p
	for _, args := range []string{"", "members", "list", "kick", "disband"} {
		if _, cmd := m.groupCommand(args); cmd != nil {
			t.Fatalf("%q invoked model", args)
		}
	}
	_, cmd := m.groupCommand("please show my teammates")
	r := m.grp.control
	m.groupCommand("")
	if r.ctx.Err() != context.Canceled {
		t.Fatal("superseded request not canceled")
	}
	if got := m.handleGroupControl(cmd().(groupControlMsg)); got != "" {
		t.Fatal("canceled completion applied")
	}
	if p.calls() > 1 {
		t.Fatal("native command called provider")
	}
}

func TestGroupControlFencesAndFailures(t *testing.T) {
	for _, mode := range []string{"session", "membership", "switch", "timeout", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			m := newGroupControlModel(t)
			m.env.LLMProvider = &autopilotStubProvider{content: `{"action":"members","arguments":{}}`}
			_, cmd := m.groupCommand("show my teammates")
			msg := cmd().(groupControlMsg)
			switch mode {
			case "session":
				msg.request.session = "other-session"
			case "membership":
				msg.request.membership = "other-membership"
			case "switch":
				m.releaseGroupOnSwitch()
			case "timeout":
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				msg.request.ctx = ctx
			case "invalid":
				msg.err = context.DeadlineExceeded
			}
			got := m.handleGroupControl(msg)
			if mode == "switch" {
				if got != "" {
					t.Fatal(got)
				}
				return
			}
			if !strings.Contains(got, "changed") && !strings.Contains(got, "no changes made") {
				t.Fatalf("unsafe outcome %q", got)
			}
		})
	}
}

func TestGroupControlDestructiveRequiresNativeCommand(t *testing.T) {
	for _, text := range []string{`{"action":"kick","arguments":{"name":"api"}}`, `{"action":"disband","arguments":{"group":"team"}}`} {
		m := newGroupControlModel(t)
		m.env.LLMProvider = &autopilotStubProvider{content: text}
		_, cmd := m.groupCommand("please remove it")
		got := m.handleGroupControl(cmd().(groupControlMsg))
		if !strings.Contains(got, "explicitly enter: /group") {
			t.Fatalf("destructive action not gated: %q", got)
		}
	}
}

func TestGroupControlAppliesOnlyOnUIThread(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := newGroupControlModel(t)
	m.services.Agent = &agent.Runtime{}
	m.services.Reminder = reminder.New()
	m.services.sessionID = "control-test"
	group.BindSession(m.sessionID)
	t.Cleanup(func() { _ = group.Leave() })
	m.env.LLMProvider = &autopilotStubProvider{replies: []string{
		`{"action":"join","arguments":{"group":"team","name":"api","role":"API work"}}`,
		`{"action":"hold","arguments":{"hold":true}}`,
		`{"action":"leave","arguments":{}}`,
	}}
	_, cmd := m.groupCommand("please join team as api")
	msg := cmd().(groupControlMsg)
	if g, _ := group.Current(); g != "" {
		t.Fatal("background completion joined group")
	}
	m.handleGroupControl(msg)
	if g, self := group.Current(); g != "team" || self.Name != "api" || self.Role != "API work" {
		t.Fatalf("join: %s %+v", g, self)
	}
	_, cmd = m.groupCommand("please hold messages")
	msg = cmd().(groupControlMsg)
	if _, self := group.Current(); self.Hold {
		t.Fatal("background completion set hold")
	}
	m.handleGroupControl(msg)
	if _, self := group.Current(); !self.Hold {
		t.Fatal("hold not applied")
	}
	_, cmd = m.groupCommand("please leave my group")
	msg = cmd().(groupControlMsg)
	if g, _ := group.Current(); g == "" {
		t.Fatal("background completion left group")
	}
	m.handleGroupControl(msg)
	if g, _ := group.Current(); g != "" {
		t.Fatal("leave not applied")
	}
}

func TestGroupControlExactJoinSyntax(t *testing.T) {
	for _, args := range []string{"", "team", "team --as @api --role owns API --hold", "--hold"} {
		if !validGroupJoinSyntax(args) {
			t.Errorf("valid: %q", args)
		}
	}
	for _, args := range []string{"the team please", "team --wat", "team --as", "team --role", "team --hold --hold"} {
		if validGroupJoinSyntax(args) {
			t.Errorf("invalid: %q", args)
		}
	}
}
