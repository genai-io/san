package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
)

// Session groups, the session's side: the /group command, and Source 4 — the
// poll that turns the group directory into roster changes and member messages
// for the main loop. The group package owns the files.

const groupUsage = `Usage:
  /group                                                     this session's group
  /group list                                                every group
  /group join [group] [--as NAME] [--role TEXT] [--passive]  join, creating the group if needed
  /group leave                                               leave your group
  /group mode active|passive                                 wake for members' messages, or wait for you
  /group kick <member>                                       remove a member
  /group disband <group>                                     remove a group; its members leave`

// groupState is what the main loop keeps about this session's group.
type groupState struct {
	blob       string                 // the membership as last persisted; "" outside a group
	name       string                 // the group blob names
	roster     map[string]rosterEntry // by session ID; nil until the first read
	stamp      time.Time              // the group directory's mtime at the last read
	fullRead   time.Time              // when member files were last read
	announced  map[string]bool        // passive messages already shown
	unattended int                    // turns member messages started since the person last typed
	userTyped  bool                   // the person's input is on its way: attach waiting messages
}

type rosterEntry struct {
	group.Member
	online bool
}

// memberMsg is one Source 4 poll: how the membership stands, the roster when
// it was re-read, who is online, and the waiting messages.
type memberMsg struct {
	status   group.Status
	members  []group.Member // nil when the directory had not changed
	stamp    time.Time
	fullRead time.Time
	online   map[string]bool // session ID → online
	inbox    []group.Message
}

// groupJoinMsg carries a /group join whose name or role had to be summarized.
type groupJoinMsg struct {
	group, name, role string
	mode              group.Mode
}

// groupTick polls once a second, off the UI goroutine. Member files are read
// only when the directory changed, or every 30 seconds in case an mtime kept to
// the second missed a change.
func groupTick(stamp, fullRead time.Time, known []group.Member) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		msg := memberMsg{status: group.Check(), stamp: stamp, fullRead: fullRead}
		if msg.status != group.Joined {
			return msg
		}
		g, _ := group.Current()
		if s, ok := group.Stamp(g); ok && (!s.Equal(stamp) || time.Since(fullRead) >= 30*time.Second) {
			msg.members, msg.stamp, msg.fullRead = group.Members(g), s, time.Now()
			known = msg.members
		}
		msg.online = make(map[string]bool, len(known))
		for _, m := range known {
			msg.online[m.SessionID] = m.Online()
		}
		msg.inbox = group.Inbox()
		return msg
	})
}

func (m *model) nextGroupTick() tea.Cmd {
	known := make([]group.Member, 0, len(m.grp.roster))
	for _, e := range m.grp.roster {
		known = append(known, e.Member)
	}
	return groupTick(m.grp.stamp, m.grp.fullRead, known)
}

func (m *model) handleMemberMsg(msg memberMsg) tea.Cmd {
	group.BindSession(m.services.Session.ID())
	var cmds []tea.Cmd
	g, self := group.Current()
	switch msg.status {
	case group.Removed, group.Disbanded:
		name := m.grp.name
		group.Forget()
		m.reconcileMembership()
		line, text := "You were removed from group "+name, "You were removed from group "+name+"; SendMessage is no longer available."
		if msg.status == group.Disbanded {
			line, text = "Group "+name+" was disbanded", "Group "+name+" was disbanded; SendMessage is no longer available."
		}
		cmds = append(cmds, m.deliverGroupReminder(line, text))
	case group.Joined:
		if self.SessionID != m.services.Session.ID() {
			// Another session took over this process (/fork): the membership
			// stays with the session that joined.
			group.Release()
			m.reconcileMembership()
			cmds = append(cmds, m.deliverGroupReminder("This session is not in group "+g, "This session is not in group "+g+"; SendMessage is no longer available."))
			break
		}
		m.reconcileMembership()
		m.grp.stamp, m.grp.fullRead = msg.stamp, msg.fullRead
		cmds = append(cmds, m.syncRoster(g, self, msg)...)
		if len(msg.inbox) > 0 {
			cmds = append(cmds, m.deliverMemberMessages(g, self, msg.inbox))
		}
	default:
		m.reconcileMembership()
	}
	return tea.Batch(append(cmds, m.nextGroupTick())...)
}

// syncRoster reports who joined, left, came online or went offline, switched
// mode or was renamed since the last poll: a line for the person and the change
// for the model. It never starts a turn.
func (m *model) syncRoster(g string, self group.Member, msg memberMsg) []tea.Cmd {
	next := map[string]rosterEntry{}
	if msg.members != nil {
		for _, mem := range msg.members {
			next[mem.SessionID] = rosterEntry{Member: mem}
		}
	} else {
		for id, e := range m.grp.roster {
			next[id] = e
		}
	}
	for id, e := range next {
		e.online = msg.online[id]
		next[id] = e
	}
	prev := m.grp.roster
	m.grp.roster = next
	if prev == nil {
		return nil // the first read after joining: the join itself told the model
	}
	var cmds []tea.Cmd
	for _, c := range rosterChanges(g, self.SessionID, prev, next) {
		cmds = append(cmds, m.deliverGroupReminder(c.line, c.text))
	}
	return cmds
}

type rosterChange struct{ line, text string } // for the person, for the model

// rosterChanges compares two roster reads: who joined, left, was renamed,
// switched mode, or went offline or came back.
func rosterChanges(g, selfID string, prev, next map[string]rosterEntry) []rosterChange {
	var out []rosterChange
	say := func(line, text string) { out = append(out, rosterChange{line, "Group " + g + ": " + text}) }
	same := func(format string, args ...any) { s := fmt.Sprintf(format, args...); say(s, s) }
	for id, now := range next {
		if id == selfID {
			continue
		}
		was, ok := prev[id]
		switch {
		case !ok:
			say(fmt.Sprintf("@%s joined group %s — %s", now.Name, g, now.Role), fmt.Sprintf("@%s joined (%s) — %s (%s)", now.Name, now.Mode, now.Role, now.Cwd))
		case was.Name != now.Name:
			same("@%s is now @%s", was.Name, now.Name)
		case was.Mode != now.Mode:
			same("@%s is now %s", now.Name, now.Mode)
		case was.online && !now.online:
			same("@%s went offline", now.Name)
		case !was.online && now.online:
			same("@%s is back online", now.Name)
		}
	}
	for id, was := range prev {
		if _, ok := next[id]; !ok && id != selfID {
			say(fmt.Sprintf("@%s left group %s", was.Name, g), fmt.Sprintf("@%s left", was.Name))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].text < out[j].text })
	return out
}

// deliverMemberMessages hands waiting messages to the agent per this member's
// mode. Active: now, starting a turn when idle, and each file is deleted once
// delivered. Passive: only a line; the files wait for the person's next input.
func (m *model) deliverMemberMessages(g string, self group.Member, msgs []group.Message) tea.Cmd {
	if self.Mode == group.Passive {
		if m.grp.announced == nil {
			m.grp.announced = map[string]bool{}
		}
		for _, msg := range msgs {
			if !m.grp.announced[msg.File] {
				m.grp.announced[msg.File] = true
				m.conv.AddAgentNotice(fromLine(msg) + " (for your next message)")
			}
		}
		return tea.Batch(m.CommitMessages()...)
	}
	if !m.conv.Stream.Active {
		m.grp.unattended++ // these start a turn with no one at the keyboard
	}
	bodies := make([]string, len(msgs))
	for i, msg := range msgs {
		bodies[i] = m.groupMessage(msg, m.grp.unattended)
	}
	display := fromLine(msgs[0])
	if len(msgs) > 1 {
		display += fmt.Sprintf(" (+%d more)", len(msgs)-1)
	}
	if m.grp.unattended >= 2 {
		display += fmt.Sprintf(" · turn %d since you last typed", m.grp.unattended)
	}
	cmd := m.deliverNotice(mainNotice{Display: display, Content: strings.Join(bodies, "\n\n"), FromAgent: true})
	group.Delivered(msgs)
	return cmd
}

// groupMessage renders one member message the way the model reads it.
func (m *model) groupMessage(msg group.Message, unattended int) string {
	from := "@" + msg.From
	for _, e := range m.grp.roster {
		if e.Name == msg.From && e.Role != "" {
			from += " (" + e.Role + ")"
		}
	}
	return fmt.Sprintf("<group-message>\nFrom: %s\nTo: @%s\nSent: %s\nUnattended-Turns: %d\n\n%s\n</group-message>",
		from, msg.To, msg.SentAt.Format("2006-01-02 15:04"), unattended, strings.TrimSpace(msg.Content))
}

// attachWaitingMessages appends a passive member's waiting messages to the
// person's input, then deletes them: they reach the conversation together.
func (m *model) attachWaitingMessages(text string) string {
	if !m.grp.userTyped {
		return text
	}
	m.grp.userTyped = false
	msgs := group.Inbox()
	if len(msgs) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	for _, msg := range msgs {
		b.WriteString("\n\n" + m.groupMessage(msg, 0))
	}
	group.Delivered(msgs)
	m.grp.announced = nil
	return b.String()
}

// deliverGroupReminder tells the model about a group change without ever
// starting a turn: into the running turn between tool calls, or with the next
// message when idle. The person gets the line either way.
func (m *model) deliverGroupReminder(line, text string) tea.Cmd {
	if m.conv.Stream.Active {
		return m.deliverNotice(mainNotice{Display: line, Content: reminder.Wrap(text)})
	}
	m.services.Reminder.Enqueue(text)
	m.conv.AddNotice(line)
	return tea.Batch(m.CommitMessages()...)
}

func fromLine(msg group.Message) string {
	first, _, _ := strings.Cut(strings.TrimSpace(msg.Content), "\n")
	return fmt.Sprintf("From @%s: %s", msg.From, kit.TruncateText(first, 80))
}

// membership is what the session record keeps, so a resume can rejoin.
type membership struct {
	Group string     `json:"group"`
	Name  string     `json:"name"`
	Role  string     `json:"role"`
	Mode  group.Mode `json:"mode"`
}

func currentMembership() string {
	g, self := group.Current()
	if g == "" {
		return ""
	}
	data, _ := json.Marshal(membership{Group: g, Name: self.Name, Role: self.Role, Mode: self.Mode})
	return string(data)
}

// reconcileMembership follows the membership wherever it changed — /group, the
// Group tool, a kick seen on disk: the session record and the roster the loop compares against.
func (m *model) reconcileMembership() {
	blob := currentMembership()
	if blob == m.grp.blob {
		return
	}
	g, _ := group.Current()
	if blob == "" {
		m.grp = groupState{}
	} else if g != m.grp.name { // joined, or a different group: start the roster afresh
		m.grp.roster, m.grp.announced = nil, nil
	}
	m.grp.blob, m.grp.name = blob, g
	_ = m.PersistSession()
}

// rejoinGroup reclaims the membership a resumed session recorded.
func (m *model) rejoinGroup(blob string) {
	group.Release() // the session this process ran before goes offline
	m.grp = groupState{}
	group.BindSession(m.services.Session.ID())
	var ms membership
	if blob == "" || json.Unmarshal([]byte(blob), &ms) != nil || ms.Group == "" {
		m.reconcileMembership()
		return
	}
	self, err := group.Reclaim(ms.Group, ms.Name)
	if err != nil {
		m.conv.AddNotice(fmt.Sprintf("Not rejoining group %s: %v", ms.Group, err))
		m.reconcileMembership()
		return
	}
	m.conv.AddNotice(fmt.Sprintf("Back in group %s as @%s (%s)", ms.Group, self.Name, self.Mode))
	m.reconcileMembership()
}

func (m *model) groupCommand(args string) (string, tea.Cmd) {
	group.BindSession(m.services.Session.ID())
	sub, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	rest = strings.TrimSpace(rest)
	switch sub {
	case "":
		return group.Listing(), nil
	case "list":
		return group.GroupsListing(), nil
	case "join":
		return m.groupJoin(rest)
	case "leave":
		g, _ := group.Current()
		if err := group.Leave(); err != nil {
			return err.Error(), nil
		}
		m.reconcileMembership()
		m.services.Reminder.Enqueue("You left group " + g + "; SendMessage is no longer available.")
		return "Left group " + g + ".", nil
	case "mode":
		mode, err := group.ParseMode(rest)
		if err != nil {
			return "Usage: /group mode active|passive", nil
		}
		if err := group.SetMode(mode); err != nil {
			return err.Error(), nil
		}
		m.reconcileMembership()
		g, _ := group.Current()
		wait := "members' messages start a turn"
		if mode == group.Passive {
			wait = "members' messages wait for your user"
		}
		m.services.Reminder.Enqueue(fmt.Sprintf("Group %s: you are now %s; %s.", g, mode, wait))
		return fmt.Sprintf("You are now %s in group %s.", mode, g), nil
	case "kick":
		if rest == "" {
			return "Usage: /group kick <member>", nil
		}
		if err := group.Kick(strings.TrimPrefix(rest, "@")); err != nil {
			return err.Error(), nil
		}
		return "Removed @" + strings.TrimPrefix(rest, "@") + ".", nil
	case "disband":
		if rest == "" {
			return "Usage: /group disband <group>", nil
		}
		mine, _ := group.Current()
		if err := group.Disband(rest); err != nil {
			return err.Error(), nil
		}
		if mine == rest {
			m.reconcileMembership()
			m.services.Reminder.Enqueue("Group " + rest + " was disbanded; SendMessage is no longer available.")
		}
		return "Disbanded group " + rest + "; its members leave.", nil
	}
	return groupUsage, nil
}

func (m *model) groupJoin(args string) (string, tea.Cmd) {
	name, member, role, mode := parseGroupJoin(args)
	if g, _ := group.Current(); g != "" {
		return fmt.Sprintf("Already in group %s; /group leave first.", g), nil
	}
	if !group.ValidName(name) {
		return fmt.Sprintf("Invalid group name %q: use letters, digits, - or _.", name), nil
	}
	if member != "" && role != "" {
		return m.finishJoin(groupJoinMsg{group: name, name: member, role: role, mode: mode}), nil
	}
	msgs := m.conv.ConvertToProviderFrom(0)
	provider, model, cwd, session := m.env.LLMProvider, m.env.GetModelID(), m.env.CWD, m.env.SessionName
	return fmt.Sprintf("Joining group %s — summarizing this session for its name and role…", name), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		gotName, gotRole := describeSession(ctx, provider, model, msgs, cwd, session)
		return groupJoinMsg{group: name, name: cmp.Or(member, gotName), role: cmp.Or(role, gotRole), mode: mode}
	}
}

// finishJoin joins and reports the outcome; it runs on the UI goroutine.
func (m *model) finishJoin(msg groupJoinMsg) string {
	group.BindSession(m.services.Session.ID())
	name := group.FreeName(msg.group, cmp.Or(group.NameFrom(msg.name), "session"))
	self, err := group.Join(msg.group, group.Member{Name: name, Role: msg.role, Mode: msg.mode, Cwd: m.env.CWD})
	if err != nil {
		return "Could not join: " + err.Error()
	}
	m.reconcileMembership()
	if m.systemRemindersSent {
		// The roster provider speaks at the start of a conversation; this one
		// is under way, so tell the model now.
		m.services.Reminder.Enqueue(group.Roster())
	}
	return fmt.Sprintf("Joined group %s as @%s (%s) — %s\n%s", msg.group, self.Name, self.Mode, self.Role, group.Listing())
}

func parseGroupJoin(args string) (name, member, role string, mode group.Mode) {
	mode = group.Active
	fields := strings.Fields(args)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "--as":
			if i+1 < len(fields) {
				member = strings.TrimPrefix(fields[i+1], "@")
				i++
			}
		case "--passive":
			mode = group.Passive
		case "--role":
			var words []string
			for i++; i < len(fields) && !strings.HasPrefix(fields[i], "--"); i++ {
				words = append(words, fields[i])
			}
			i--
			role = strings.Trim(strings.Join(words, " "), `"'`)
		default:
			if name == "" {
				name = fields[i]
			}
		}
	}
	return cmp.Or(name, group.DefaultName), member, role, mode
}

const describeSessionPrompt = `You name a coding session that is joining a group of collaborating sessions.
Read the conversation and answer with exactly two lines, nothing else:
name: <a 1-3 word kebab-case handle for what this session works on>
role: <one sentence: what this session is doing and what it owns>`

// describeSession summarizes the conversation into a member name and role,
// falling back to the session name or directory when there is nothing to go on.
func describeSession(ctx context.Context, provider llm.Provider, model string, msgs []core.Message, cwd, session string) (name, role string) {
	name = cmp.Or(group.NameFrom(session), group.NameFrom(filepath.Base(cwd)), "session")
	role = "working in " + filepath.Base(cwd)
	if provider == nil || len(msgs) == 0 {
		return name, role
	}
	text := core.BuildCompactionText(msgs)
	if len(text) > 12000 {
		text = text[len(text)-12000:]
	}
	resp, err := llm.Complete(ctx, provider, llm.CompletionOptions{
		Model:        model,
		SystemPrompt: describeSessionPrompt,
		Messages:     []core.Message{core.UserMessage(text, nil)},
		MaxTokens:    600,
	})
	if err != nil {
		return name, role
	}
	for line := range strings.SplitSeq(resp.Content.Text(), "\n") {
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			if n := group.NameFrom(value); n != "" {
				name = n
			}
		case "role":
			if value != "" {
				role = value
			}
		}
	}
	return name, role
}

// groupSuggestions completes /group: its subcommands, then what each takes.
func groupSuggestions(args string) []suggest.Suggestion {
	sub, rest, hasSub := strings.Cut(args, " ")
	if !hasSub {
		var out []suggest.Suggestion
		for _, s := range []suggest.Suggestion{
			{Name: "group join", Description: "join or create a group"},
			{Name: "group leave", Description: "leave your group"},
			{Name: "group mode", Description: "active: wake for members · passive: wait for you"},
			{Name: "group kick", Description: "remove a member"},
			{Name: "group list", Description: "every group"},
			{Name: "group disband", Description: "remove a group; its members leave"},
		} {
			if strings.HasPrefix(strings.TrimPrefix(s.Name, "group "), sub) {
				out = append(out, s)
			}
		}
		return out
	}
	if strings.Contains(rest, " ") {
		return nil
	}
	var out []suggest.Suggestion
	add := func(value, desc string) {
		if strings.HasPrefix(value, rest) {
			out = append(out, suggest.Suggestion{Name: "group " + sub + " " + value, Description: desc})
		}
	}
	switch sub {
	case "join", "disband":
		for _, g := range group.Groups() {
			members := group.Members(g)
			names := make([]string, len(members))
			for i, mem := range members {
				names[i] = "@" + mem.Name
			}
			add(g, fmt.Sprintf("%d · %s", len(members), strings.Join(names, " ")))
		}
		if sub == "join" && len(out) == 0 {
			add(group.DefaultName, "create it")
		}
	case "mode":
		add("active", "members' messages start a turn")
		add("passive", "members' messages wait for you")
	case "kick":
		g, self := group.Current()
		members := group.Members(g)
		for _, mem := range members {
			if mem.SessionID != self.SessionID {
				add(mem.Name, map[bool]string{true: "online", false: "offline"}[mem.Online()]+" · "+mem.Role)
			}
		}
	}
	return out
}
