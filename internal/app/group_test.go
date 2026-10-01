package app

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/session"
)

func TestParseGroupJoin(t *testing.T) {
	type want struct {
		name, member, role string
		mode               group.Mode
	}
	for args, w := range map[string]want{
		"":               {"default", "", "", group.Active},
		"shop --passive": {"shop", "", "", group.Passive},
		"shop --as @web --role owns the checkout": {"shop", "web", "owns the checkout", group.Active},
		`--role "runs 0042" --as migrate`:         {"default", "migrate", "runs 0042", group.Active},
	} {
		name, member, role, mode := parseGroupJoin(args)
		if got := (want{name, member, role, mode}); got != w {
			t.Errorf("parseGroupJoin(%q) = %+v, want %+v", args, got, w)
		}
	}
}

func TestGroupSuggestionsWalkSubcommandsThenValues(t *testing.T) {
	names := func(args string) []string {
		var out []string
		for _, s := range groupSuggestions(args) {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names(""); !slices.Equal(got, []string{"group join", "group list", "group disband"}) {
		t.Errorf("outside a group /group offers %v, want only what applies", got)
	}
	if got := names("join shop --as web "); !slices.Equal(got, []string{"group join shop --as web --passive", "group join shop --as web --role"}) {
		t.Errorf("/group join shop --as web offers %v, want the flags not yet given", got)
	}
	if got := names("join shop --role "); got != nil {
		t.Errorf("a value comes after --role, offered %v", got)
	}
	if got := names("mode "); !slices.Equal(got, []string{"group mode active", "group mode passive"}) {
		t.Errorf("/group mode offers %v", got)
	}
	if got := names("join "); len(got) == 0 {
		t.Error("/group join offers no group")
	}
	if got := names("list x"); got != nil {
		t.Errorf("list takes no argument, offered %v", got)
	}
}

func TestRosterChangesSayWhatHappened(t *testing.T) {
	entry := func(name string, mode group.Mode, online bool) rosterEntry {
		return rosterEntry{Member: group.Member{Name: name, Mode: mode, Role: "r", Cwd: "/w"}, online: online}
	}
	prev := map[string]rosterEntry{
		"s-self": entry("web", group.Active, true),
		"s-api":  entry("api", group.Active, true),
		"s-mig":  entry("migrate", group.Active, true),
		"s-gone": entry("old", group.Active, true),
	}
	next := map[string]rosterEntry{
		"s-self": entry("web", group.Passive, true), // own changes are not reported
		"s-api":  entry("api", group.Active, false),
		"s-mig":  entry("migrate", group.Passive, true),
		"s-new":  entry("qa", group.Active, true),
	}
	var got []string
	for _, c := range rosterChanges("shop", "s-self", prev, next) {
		got = append(got, c.line+" | "+c.text)
	}
	// Presence and mode are shown, not told: SendMessage's result says them.
	want := []string{
		"@api went offline | ",
		"@migrate is now passive | ",
		"@old left group shop | Group shop: @old left",
		"@qa joined group shop — r | Group shop: @qa joined (active) — r (/w)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rosterChanges =\n%q\nwant\n%q", got, want)
	}
}

func TestAPassiveMembersMessagesRideOnTheNextInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group.BindSession("s-self")
	if _, err := group.Join("shop", group.Member{Name: "migrate", Mode: group.Passive}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = group.Leave() })
	home, _ := os.UserHomeDir()
	inbox := filepath.Join(home, ".san", "groups", "shop", "migrate.inbox")
	msg := group.Message{From: "api", To: "migrate", Content: "run 0043 next", SentAt: time.Now()}
	if err := atomicfile.WriteJSON(filepath.Join(inbox, "1-api.json"), msg, 0o600); err != nil {
		t.Fatal(err)
	}

	m := &model{}
	if got := m.attachWaitingMessages("is the migration done?"); got != "is the migration done?" {
		t.Errorf("attached without the person typing: %q", got)
	}
	m.grp.userTyped = true
	got := m.attachWaitingMessages("is the migration done?")
	for _, want := range []string{"is the migration done?\n\n<group-message>", "From: @api", "To: @migrate", "Unattended-Turns: 0", "run 0043 next"} {
		if !strings.Contains(got, want) {
			t.Errorf("attached input lacks %q:\n%s", want, got)
		}
	}
	if left := group.Inbox(); len(left) != 0 {
		t.Errorf("delivered messages were left in the inbox: %+v", left)
	}
}

// A session never saved can't be resumed; at exit its member leaves rather
// than staying offline for good.
func TestAnUnsavedSessionLeavesItsGroupAtExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	group.BindSession("s-new")
	if _, err := group.Join("shop", group.Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	m := model{services: services{Session: &session.Setup{}}}
	m.exitGroup()
	if members := group.Members("shop"); len(members) != 0 {
		t.Errorf("members after exit = %+v, want none", members)
	}
}

func TestMemberColorsGoInOrderOfArrivalAndStay(t *testing.T) {
	at := func(min int) time.Time { return time.Date(2026, 9, 30, 10, min, 0, 0, time.UTC) }
	entry := func(id, name string, min int) rosterEntry {
		return rosterEntry{Member: group.Member{SessionID: id, Name: name, JoinedAt: at(min)}}
	}
	var m model
	m.placeMemberColors(map[string]rosterEntry{"s-self": entry("s-self", "web", 0), "s-2": entry("s-2", "qa", 2), "s-1": entry("s-1", "api", 1)}, "s-self")
	m.placeMemberColors(map[string]rosterEntry{"s-0": entry("s-0", "aaa", 3), "s-1": entry("s-1", "api", 1)}, "s-self")
	want := map[string]int{"api": 0, "qa": 1, "aaa": 2}
	if !maps.Equal(m.grp.colors, want) {
		t.Errorf("colors = %v, want %v (self skipped, earlier members keep theirs)", m.grp.colors, want)
	}
}

// The loop runs only in a group: none outside one, one per membership, and it
// ends with the tick that finds the session gone from its group.
func TestTheGroupLoopRunsOnlyWhileInAGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := &model{services: services{Session: &session.Setup{}}}
	if m.startGroupPolling() != nil {
		t.Fatal("a loop started outside any group")
	}
	group.BindSession("s-self")
	if _, err := group.Join("shop", group.Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if m.startGroupPolling() == nil || m.startGroupPolling() != nil {
		t.Fatal("want exactly one loop once in a group")
	}
	_ = group.Leave()
	if next := m.handleMemberMsg(memberMsg{status: group.NotJoined}); m.grp.polling || next != nil {
		t.Error("the loop outlived the membership")
	}
	group.BindSession("s-self")
	if _, err := group.Join("shop", group.Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = group.Leave() })
	if m.startGroupPolling() == nil {
		t.Error("rejoining did not start the loop again")
	}
}
