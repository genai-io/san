package group

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/proc"
)

// isolate points the package at a fresh root and leaves no membership behind.
func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	root = func() string { return dir }
	BindSession("s-self")
	t.Cleanup(func() { setCurrent("", Member{}) })
}

// writeMember writes a member file the way another process would.
func writeMember(t *testing.T, g string, m Member) {
	t.Helper()
	if err := os.MkdirAll(inboxDir(g, m.Name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteJSON(memberFile(g, m.Name), m, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestANameIsClaimedOnce(t *testing.T) {
	isolate(t)
	writeMember(t, "shop", Member{Name: "web", SessionID: "s-other"})
	if _, err := Join("shop", Member{Name: "web"}); err == nil || !strings.Contains(err.Error(), "already taken") {
		t.Fatalf("joining under a taken name: err = %v", err)
	}
	if _, err := Join("shop", Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Join("shop", Member{Name: "again"}); err == nil {
		t.Error("a second join from the same process was accepted")
	}
}

func TestAMessageStaysUntilDelivered(t *testing.T) {
	isolate(t)
	if _, err := Join("shop", Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	msg := Message{From: "web", To: "api", Content: "rebased", SentAt: time.Now()}
	if err := atomicfile.WriteJSON(filepath.Join(inboxDir("shop", "api"), "1-web.json"), msg, 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 { // reading twice finds it twice: reading is not delivering
		if got := Inbox(); len(got) != 1 || got[0].Content != "rebased" {
			t.Fatalf("Inbox = %+v, want the one message", got)
		}
	}
	Delivered(Inbox())
	if got := Inbox(); len(got) != 0 {
		t.Errorf("Inbox after delivery = %+v, want empty", got)
	}
}

func TestSendNamesTheMembersWhenTheNameIsWrong(t *testing.T) {
	isolate(t)
	writeMember(t, "shop", Member{Name: "web", Mode: Passive})
	if _, err := Join("shop", Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Send("front", "hi"); err == nil || !strings.Contains(err.Error(), "members: web") {
		t.Errorf("unknown name: err = %v, want the member list", err)
	}
	if _, err := Send("api", "hi"); err == nil {
		t.Error("sending to yourself was accepted")
	}
	to, err := Send("web", "schema changed")
	if err != nil || to.Mode != Passive {
		t.Fatalf("Send = %+v, %v; want web, passive", to, err)
	}
	if entries, _ := os.ReadDir(inboxDir("shop", "web")); len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "-api.json") {
		t.Errorf("web's inbox = %v, want one <time>-api.json", entries)
	}
}

func TestAMemberNoticesBeingKickedOrDisbanded(t *testing.T) {
	isolate(t)
	writeMember(t, "shop", Member{Name: "web"})
	if _, err := Join("shop", Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if Check() != Joined {
		t.Fatal("a fresh member does not read as joined")
	}
	_ = os.Remove(memberFile("shop", "api")) // another session ran /group kick api
	if Check() != Removed {
		t.Errorf("Check after kick = %v, want Removed", Check())
	}
	_ = os.RemoveAll(groupDir("shop")) // another session ran /group disband shop
	if Check() != Disbanded {
		t.Errorf("Check after disband = %v, want Disbanded", Check())
	}
}

func TestAResumedSessionReclaimsItsMemberOnlyIfItIsOffline(t *testing.T) {
	isolate(t)
	writeMember(t, "shop", Member{Name: "web", SessionID: "s-self"}) // pid 0: offline
	self, err := Reclaim("shop", "web")
	if err != nil || self.PID != os.Getpid() || !self.Online() {
		t.Fatalf("Reclaim = %+v, %v; want this process, online", self, err)
	}
	Release()
	var onDisk Member
	_ = readJSON(memberFile("shop", "web"), &onDisk)
	if onDisk.Online() {
		t.Error("a released member still reads as online")
	}

	// The same session, still running in another process.
	start, ok := proc.StartTime(os.Getppid())
	if !ok {
		t.Skip("no parent process to stand in for another terminal")
	}
	writeMember(t, "shop", Member{Name: "web", SessionID: "s-self", PID: os.Getppid(), ProcStart: start})
	if _, err := Reclaim("shop", "web"); !errors.Is(err, ErrOnlineElsewhere) {
		t.Errorf("Reclaim while online elsewhere: err = %v, want ErrOnlineElsewhere", err)
	}

	writeMember(t, "shop", Member{Name: "web", SessionID: "s-fork"})
	if _, err := Reclaim("shop", "web"); err == nil {
		t.Error("reclaimed a member that belongs to another session")
	}
}

func TestAReusedPidIsNotOnline(t *testing.T) {
	m := Member{PID: os.Getpid(), ProcStart: "started-by-someone-else"}
	if m.Online() {
		t.Error("a pid with a different start time reads as online")
	}
}

func TestTheLastMemberLeavingRemovesTheGroup(t *testing.T) {
	isolate(t)
	if _, err := Join("solo", Member{Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := Leave(); err != nil {
		t.Fatal(err)
	}
	if groups := Groups(); len(groups) != 0 {
		t.Errorf("Groups = %v after the last member left, want none", groups)
	}
}
