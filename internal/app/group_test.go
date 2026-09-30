package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/group"
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
	if got := names(""); len(got) != 6 {
		t.Errorf("bare /group offers %v, want the six subcommands", got)
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
		"s-docs": entry("docs", group.Active, false),
		"s-gone": entry("old", group.Active, true),
	}
	next := map[string]rosterEntry{
		"s-self": entry("web", group.Passive, true), // own changes are not reported
		"s-api":  entry("api", group.Active, false),
		"s-mig":  entry("migrate", group.Passive, true),
		"s-docs": entry("writer", group.Active, false),
		"s-new":  entry("qa", group.Active, true),
	}
	var got []string
	for _, c := range rosterChanges("shop", "s-self", prev, next) {
		got = append(got, c.text)
	}
	want := []string{
		"Group shop: @api went offline",
		"Group shop: @docs is now @writer",
		"Group shop: @migrate is now passive",
		"Group shop: @old left",
		"Group shop: @qa joined (active) — r (/w)",
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
