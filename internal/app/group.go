package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
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
  /group members                                             who is in your group
  /group list                                                every group
  /group join [group] [--as NAME] [--role TEXT] [--hold]     join, creating the group if needed
  /group leave                                               leave your group
  /group hold on|off                                         keep members' messages until you type, or not
  /group kick <member>                                       remove a member
  /group disband <group>                                     remove a group; its members leave`

// groupState is what the main loop keeps about this session's group.
type groupState struct {
	blob       string                 // the membership as last persisted; "" outside a group
	name       string                 // the group blob names
	roster     map[string]rosterEntry // by session ID; nil until the first read
	stamp      time.Time              // the group directory's mtime at the last read
	fullRead   time.Time              // when member files were last read
	announced  map[string]bool        // held messages already shown
	unattended int                    // turns member messages started since the person last typed
	userTyped  bool                   // the person's input is on its way: attach waiting messages
	colors     map[string]int         // member name → palette slot, in order of arrival, never reassigned
	// polling: the one-second loop is scheduled. It is cleared only by a tick
	// that finds the session out of its group and schedules no next, so no
	// tick is ever in flight while it is false, and there is never two loops.
	// It outlives the resets above: it is the loop, not the membership.
	polling bool
}

type rosterEntry struct {
	group.Member
	online bool
}

// memberMsg is one Source 4 poll: how the membership stands, the roster when
// with who is online, and the waiting messages.
type memberMsg struct {
	status   group.Status
	roster   map[string]rosterEntry // by session ID
	stamp    time.Time
	fullRead time.Time
	inbox    []group.Message
}

// groupJoinMsg carries a /group join whose name or role had to be summarized.
type groupJoinMsg struct {
	group, name, role string
	hold              bool
}

// groupTick polls once a second, off the UI goroutine. Member files are read
// only when the directory changed, or every 30 seconds in case an mtime kept to
// the second missed a change.
func groupTick(stamp, fullRead time.Time, known map[string]rosterEntry) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		msg := memberMsg{status: group.Check(), stamp: stamp, fullRead: fullRead}
		if msg.status != group.Joined {
			return msg
		}
		g, _ := group.Current()
		if s, ok := group.Stamp(g); ok && (!s.Equal(stamp) || time.Since(fullRead) >= 30*time.Second) {
			known, msg.stamp, msg.fullRead = map[string]rosterEntry{}, s, time.Now()
			for _, mem := range group.Members(g) {
				known[mem.SessionID] = rosterEntry{Member: mem}
			}
		}
		msg.roster = make(map[string]rosterEntry, len(known)) // a new map: known is the UI's
		for id, e := range known {
			e.online = e.Online()
			msg.roster[id] = e
		}
		msg.inbox = group.Inbox()
		return msg
	})
}

func (m *model) nextGroupTick() tea.Cmd { return groupTick(m.grp.stamp, m.grp.fullRead, m.grp.roster) }

// startGroupPolling starts the loop once this session is in a group, however
// it got there: /group join, the Group tool, or a resume. Update calls it
// after every message; outside a group it costs one lock and does nothing.
func (m *model) startGroupPolling() tea.Cmd {
	if m.grp.polling {
		return nil
	}
	if g, _ := group.Current(); g == "" {
		return nil
	}
	m.grp.polling = true
	return m.nextGroupTick()
}

// syncGroupState keeps this member's state on disk in step with the session,
// writing only on a change, so other members know when a message will be read.
func (m *model) syncGroupState() {
	g, self := group.Current()
	if g == "" {
		return
	}
	state := group.Idle
	switch {
	case m.services.Agent.PendingPermission() != nil:
		state = group.Approval
	case m.conv.Stream.Active:
		state = group.Working
	}
	if state != self.State {
		_ = group.SetState(state)
	}
}

// groupSelfName is this session's member name, for the status bar.
func (m *model) groupSelfName() string {
	_, self := group.Current()
	return self.Name
}

// groupHolds reports whether this member holds messages, for the status bar.
func (m *model) groupHolds() bool {
	_, self := group.Current()
	return self.Hold
}

// membersAwaitingApproval names the other members whose turn waits on their
// user, for the status bar.
func (m *model) membersAwaitingApproval() []string {
	_, self := group.Current()
	var names []string
	for _, e := range m.grp.roster {
		if e.online && e.State == group.Approval && e.SessionID != self.SessionID {
			names = append(names, "@"+e.Name)
		}
	}
	slices.Sort(names)
	return names
}

func (m *model) handleMemberMsg(msg memberMsg) tea.Cmd {
	var cmds []tea.Cmd
	g, self := group.Current()
	switch msg.status {
	case group.Removed, group.Disbanded:
		name := m.grp.name
		group.Forget()
		m.reconcileMembership()
		line := "You were removed from group " + name
		if msg.status == group.Disbanded {
			line = "Group " + name + " was disbanded"
		}
		cmds = append(cmds, m.deliverGroupReminder(line, line+group.NoSendMessage))
	case group.Joined:
		if self.SessionID != m.services.Session.ID() {
			// Another session took over this process (/fork): the membership
			// stays with the session that joined.
			group.Release()
			m.reconcileMembership()
			line := "This session is not in group " + g
			cmds = append(cmds, m.deliverGroupReminder(line, line+group.NoSendMessage))
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
	if g, _ := group.Current(); g == "" {
		m.grp.polling = false // out of the group: the loop ends here
		return tea.Batch(cmds...)
	}
	return tea.Batch(append(cmds, m.nextGroupTick())...)
}

// syncRoster reports who joined, left, came online or went offline, or
// switched mode since the last poll: a line for the person and the change
// for the model. It never starts a turn.
func (m *model) syncRoster(g string, self group.Member, msg memberMsg) []tea.Cmd {
	next := msg.roster
	prev := m.grp.roster
	m.grp.roster = next
	m.placeMemberColors(next, self.SessionID)
	if prev == nil {
		return nil // the first read after joining: the join itself told the model
	}
	var cmds []tea.Cmd
	for _, c := range rosterChanges(g, self.SessionID, prev, next) {
		cmds = append(cmds, m.deliverGroupReminder(c.line, c.text))
	}
	return cmds
}

// placeMemberColors gives each member not yet seen the next colour, oldest
// first, so members never share one below the palette's size and a colour,
// once on screen, keeps meaning the same member.
func (m *model) placeMemberColors(roster map[string]rosterEntry, selfID string) {
	if m.grp.colors == nil {
		m.grp.colors = map[string]int{}
	}
	members := slices.Collect(maps.Values(roster))
	slices.SortFunc(members, func(a, b rosterEntry) int { return a.JoinedAt.Compare(b.JoinedAt) })
	for _, e := range members {
		if _, ok := m.grp.colors[e.Name]; !ok && e.SessionID != selfID {
			m.grp.colors[e.Name] = len(m.grp.colors)
		}
	}
}

type rosterChange struct{ line, text string } // for the person, for the model

// rosterChanges compares two roster reads: who joined, left, switched mode,
// or went offline or came back.
func rosterChanges(g, selfID string, prev, next map[string]rosterEntry) []rosterChange {
	var out []rosterChange
	say := func(line, text string) { out = append(out, rosterChange{line, "Group " + g + ": " + text}) }
	// Holding and presence are for the person: the model learns them when it
	// sends, from SendMessage's result.
	show := func(format string, args ...any) { out = append(out, rosterChange{line: fmt.Sprintf(format, args...)}) }
	for id, now := range next {
		if id == selfID {
			continue
		}
		was, ok := prev[id]
		switch {
		case !ok:
			say(fmt.Sprintf("@%s joined — %s", now.Name, now.Role), fmt.Sprintf("@%s joined%s — %s (%s)", now.Name, holdsNote(now.Hold), now.Role, now.Cwd))
		case !was.Hold && now.Hold:
			show("@%s now holds messages", now.Name)
		case was.Hold && !now.Hold:
			show("@%s stopped holding messages", now.Name)
		case was.online && !now.online:
			show("@%s went offline", now.Name)
		case !was.online && now.online:
			show("@%s came back online", now.Name)
		}
	}
	for id, was := range prev {
		if _, ok := next[id]; !ok && id != selfID {
			say("@"+was.Name+" left", "@"+was.Name+" left")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// deliverMemberMessages hands waiting messages to the agent: now, starting a
// turn when idle, each file deleted once delivered — or, when this member
// holds messages, only a line, the files waiting for the person's next input.
func (m *model) deliverMemberMessages(g string, self group.Member, msgs []group.Message) tea.Cmd {
	if self.Hold {
		if m.grp.announced == nil {
			m.grp.announced = map[string]bool{}
		}
		for _, msg := range msgs {
			if !m.grp.announced[msg.File] {
				m.grp.announced[msg.File] = true
				m.conv.AddAgentNotice(fromLine(msg) + " · waits for you")
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
		display += fmt.Sprintf(" · unattended %d", m.grp.unattended)
	}
	cmd := m.deliverNotice(mainNotice{Display: display, Content: strings.Join(bodies, "\n\n"), FromAgent: true})
	group.Delivered(msgs)
	return cmd
}

// groupMessage renders one member message the way the model reads it. From
// names the sender only: its role is in the roster.
func (m *model) groupMessage(msg group.Message, unattended int) string {
	return fmt.Sprintf("<group-message>\nFrom: @%s\nTo: @%s\nSent: %s\nUnattended-Turns: %d\n\n%s\n</group-message>",
		msg.From, msg.To, msg.SentAt.Format("2006-01-02 15:04"), unattended, strings.TrimSpace(msg.Content))
}

// attachWaitingMessages appends a holding member's waiting messages to the
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
	line = m.fitLines(line)
	if m.conv.Stream.Active {
		content := "" // "" text: for the person only
		if text != "" {
			content = reminder.Wrap(text)
		}
		return m.deliverNotice(mainNotice{Display: line, Content: content})
	}
	if text != "" && m.systemRemindersSent {
		// Before that, the first message carries the whole roster instead.
		m.services.Reminder.Enqueue(text)
	}
	m.conv.AddNotice(line)
	return tea.Batch(m.CommitMessages()...)
}

func fromLine(msg group.Message) string {
	first, _, _ := strings.Cut(strings.TrimSpace(msg.Content), "\n")
	return fmt.Sprintf("From @%s: %s", msg.From, kit.TruncateText(first, 80))
}

// membership is what the session record keeps, so a resume can rejoin.
type membership struct {
	Group string `json:"group"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Hold  bool   `json:"hold,omitempty"`
}

func currentMembership() string {
	g, self := group.Current()
	if g == "" {
		return ""
	}
	data, _ := json.Marshal(membership{Group: g, Name: self.Name, Role: self.Role, Hold: self.Hold})
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
		m.grp = groupState{polling: m.grp.polling}
	} else if g != m.grp.name { // joined, or a different group: start the roster afresh
		m.grp.roster, m.grp.announced = nil, nil
	}
	m.grp.blob, m.grp.name = blob, g
	_ = m.PersistSession()
}

// exitGroup takes the member offline at exit. A session that was never saved
// can't be resumed, so its member would stay offline for good: it leaves.
func (m *model) exitGroup() {
	if path := m.services.Session.TranscriptPath(); path != "" {
		if _, err := os.Stat(path); err == nil {
			group.Release()
			return
		}
	}
	_ = group.Leave()
}

// rejoinGroup reclaims the membership a resumed session recorded.
func (m *model) rejoinGroup(blob string) {
	group.Release() // the session this process ran before goes offline
	m.grp = groupState{polling: m.grp.polling}
	var ms membership
	if blob != "" && json.Unmarshal([]byte(blob), &ms) == nil && ms.Group != "" {
		if self, err := group.Reclaim(ms.Group, ms.Name); err != nil {
			m.conv.AddNotice(fmt.Sprintf("Not rejoining group %s: %v", ms.Group, err))
		} else {
			m.conv.AddNotice(fmt.Sprintf("Back in group %s as @%s%s", ms.Group, self.Name, holdTag(self.Hold)))
		}
	}
	m.reconcileMembership()
}

func (m *model) groupCommand(args string) (string, tea.Cmd) {
	sub, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	rest = strings.TrimSpace(rest)
	switch sub {
	case "", "members":
		return m.fitLines(group.Listing()), nil
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
		m.services.Reminder.Enqueue("You left group " + g + group.NoSendMessage)
		return "Left group " + g + ".", nil
	case "hold":
		if rest != "on" && rest != "off" {
			return "Usage: /group hold on|off", nil
		}
		hold := rest == "on"
		if err := group.SetHold(hold); err != nil {
			return err.Error(), nil
		}
		m.reconcileMembership()
		// Holding only changes when messages are delivered; the model is not
		// told, as with presence.
		g, _ := group.Current()
		if hold {
			return "Holding members' messages in " + g + " until you type.", nil
		}
		return "Members' messages in " + g + " are acted on right away again.", nil
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
			m.services.Reminder.Enqueue("Group " + rest + " was disbanded" + group.NoSendMessage)
		}
		return "Disbanded group " + rest + "; its members leave.", nil
	}
	return groupUsage, nil
}

func (m *model) groupJoin(args string) (string, tea.Cmd) {
	name, member, role, hold := parseGroupJoin(args)
	if g, _ := group.Current(); g != "" {
		return fmt.Sprintf("Already in group %s; /group leave first.", g), nil
	}
	if !group.ValidName(name) {
		return fmt.Sprintf("Invalid group name %q: use letters, digits, - or _.", name), nil
	}
	msgs := m.conv.ConvertToProviderFrom(0)
	provider, model, cwd, session := m.env.LLMProvider, m.env.GetModelID(), m.env.CWD, m.env.SessionName
	if (member != "" && role != "") || len(msgs) == 0 { // given, or nothing to summarize
		gotName, gotRole := group.Fallback(session, cwd)
		return m.finishJoin(groupJoinMsg{group: name, name: cmp.Or(member, gotName), role: cmp.Or(role, gotRole), hold: hold}), nil
	}
	return m.fitLines(fmt.Sprintf("Joining %s — naming this session…", name)), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		gotName, gotRole := describeSession(ctx, provider, model, msgs, cwd, session)
		return groupJoinMsg{group: name, name: cmp.Or(member, gotName), role: cmp.Or(role, gotRole), hold: hold}
	}
}

// finishJoin joins and reports the outcome; it runs on the UI goroutine.
func (m *model) finishJoin(msg groupJoinMsg) string {
	name := group.FreeName(msg.group, cmp.Or(group.NameFrom(msg.name), "session"))
	self, err := group.Join(msg.group, group.Member{Name: name, Role: msg.role, Hold: msg.hold, Cwd: m.env.CWD})
	if err != nil {
		return "Could not join: " + err.Error()
	}
	m.reconcileMembership()
	if m.systemRemindersSent {
		// The roster provider speaks at the start of a conversation; this one
		// is under way, so tell the model now.
		m.services.Reminder.Enqueue(group.Roster())
	}
	return m.fitLines(fmt.Sprintf("Joined %s as @%s · %s%s", msg.group, self.Name, group.MemberCount(len(group.Members(msg.group))), holdTag(self.Hold)))
}

// joinFlagSuggestions offers the join flags not yet given, once a group is named.
func joinFlagSuggestions(rest string) []suggest.Suggestion {
	done, partial := rest[:strings.LastIndex(rest, " ")+1], rest[strings.LastIndex(rest, " ")+1:]
	fields := strings.Fields(done)
	if last := fields[len(fields)-1]; last == "--as" || last == "--role" {
		return nil // a value comes next
	}
	var out []suggest.Suggestion
	for _, f := range []struct{ flag, desc string }{
		{"--hold", "keep members' messages until you type"},
		{"--as", "NAME · your member name"},
		{"--role", "TEXT · what this session owns"},
	} {
		if strings.HasPrefix(f.flag, partial) && !slices.Contains(fields, f.flag) {
			out = append(out, suggest.Suggestion{Name: "group join " + done + f.flag, Description: f.desc})
		}
	}
	return out
}

// holdTag marks a member that holds messages, in what the person reads.
func holdTag(hold bool) string {
	if hold {
		return " · hold"
	}
	return ""
}

// holdsNote marks one in what the model reads.
func holdsNote(hold bool) string {
	if hold {
		return " (holds messages)"
	}
	return ""
}

// fitLines cuts each line to the screen: notices are not re-wrapped, so a
// long one would spill onto an unindented line and a listing lose its columns.
func (m *model) fitLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = kit.TruncateText(line, m.env.Width-4)
	}
	return strings.Join(lines, "\n")
}

func parseGroupJoin(args string) (name, member, role string, hold bool) {
	fields := strings.Fields(args)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "--as":
			if i+1 < len(fields) {
				member = strings.TrimPrefix(fields[i+1], "@")
				i++
			}
		case "--hold":
			hold = true
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
	return cmp.Or(name, group.DefaultName), member, role, hold
}

const describeSessionPrompt = `You name a coding session that is joining a group of collaborating sessions.
Read the conversation and answer with exactly two lines, nothing else:
name: <a 1-3 word kebab-case handle for what this session works on>
role: <at most 10 words on what this session owns, in the conversation's language>`

// describeSession summarizes the conversation into a member name and role,
// falling back to the session name or directory when there is nothing to go on.
func describeSession(ctx context.Context, provider llm.Provider, model string, msgs []core.Message, cwd, session string) (name, role string) {
	name, role = group.Fallback(session, cwd)
	if provider == nil || len(msgs) == 0 {
		return name, role
	}
	text := kit.TruncateKeepEnd(core.BuildCompactionText(msgs), 12000)
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
		// Only what applies now: join outside a group, the rest inside one.
		g, _ := group.Current()
		joined := g != ""
		var out []suggest.Suggestion
		for _, s := range []struct {
			sub, desc string
			show      bool
		}{
			{"members", "who is in your group, and what each is doing", joined},
			{"join", "join or create a group", !joined},
			{"leave", "leave your group", joined},
			{"hold", "on: keep members' messages until you type · off: act on them now", joined},
			{"kick", "remove a member", joined},
			{"list", "every group", true},
			{"disband", "remove a group; its members leave", true},
		} {
			if s.show && strings.HasPrefix(s.sub, sub) {
				out = append(out, suggest.Suggestion{Name: "group " + s.sub, Description: s.desc})
			}
		}
		return out
	}
	if strings.Contains(rest, " ") {
		if sub == "join" {
			return joinFlagSuggestions(rest)
		}
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
	case "hold":
		add("on", "keep members' messages until you type")
		add("off", "act on members' messages right away")
	case "kick":
		g, self := group.Current()
		members := group.Members(g)
		for _, mem := range members {
			if mem.SessionID != self.SessionID {
				state := "offline"
				if mem.Online() {
					state = "online"
				}
				add(mem.Name, state+" · "+mem.Role)
			}
		}
	}
	return out
}
