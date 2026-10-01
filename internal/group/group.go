// Package group lets sessions on one machine message each other. A group is a
// directory; each member owns one file in it and one inbox beside it:
//
//	~/.san/groups/<group>/<name>.json    who the member is
//	~/.san/groups/<group>/<name>.inbox/  one file per message addressed to it
//
// Nothing is shared-written: a member writes only its own file, and a sender
// only adds files to the recipient's inbox. A message file is the message's
// only copy and is deleted once it has reached the conversation, so an exit
// or crash in between loses nothing.
//
// Membership belongs to a session, not a process: a process that exits leaves
// its member offline, and resuming the session reclaims it. A process holds
// at most one membership at a time, kept as package state the way broker keeps
// its routes.
package group

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/proc"
)

// DefaultName is the group joined when none is named.
const DefaultName = "default"

// Member is one session in a group, as its member file records it.
type Member struct {
	Name string `json:"name"`
	Role string `json:"role"`
	// Hold keeps the other members' messages until this member's user next
	// types; without it they are acted on right away.
	Hold      bool      `json:"hold,omitempty"`
	SessionID string    `json:"sessionID"`
	PID       int       `json:"pid"`
	ProcStart string    `json:"procStart"`
	Cwd       string    `json:"cwd"`
	JoinedAt  time.Time `json:"joinedAt"`
	State     State     `json:"state,omitempty"`
}

// State is what a member's session is doing, so a sender knows when a message
// will be read, and the person sees who is stuck on them.
type State string

const (
	Idle     State = "idle"
	Working  State = "working"  // a turn is running
	Approval State = "approval" // a turn waits on its user to approve a tool call
)

// Online reports whether the member's process is still the one that wrote the
// file: a pid the system has since handed to another process does not count.
func (m Member) Online() bool {
	if m.PID <= 0 {
		return false
	}
	start, ok := proc.StartTime(m.PID)
	return ok && start == m.ProcStart
}

// Message is one message in an inbox. File is where it lies, so it can be
// deleted once delivered.
type Message struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	Content string    `json:"content"`
	SentAt  time.Time `json:"sentAt"`
	File    string    `json:"-"`
}

// Status is how this process's membership stands on disk.
type Status int

const (
	NotJoined Status = iota
	Joined
	Removed   // its member file is gone or taken over: it was kicked
	Disbanded // the group directory is gone
)

// NoSendMessage ends every notice that this session is out of its group.
const NoSendMessage = "; SendMessage is no longer available."

var (
	ErrOnlineElsewhere = errors.New("this session is already online elsewhere")
	errNotJoined       = errors.New("this session is not in a group")

	// root is ~/.san/groups; a variable so tests can point it elsewhere.
	root = func() string {
		home, _ := os.UserHomeDir()
		return filepath.Join(confdir.Dir(home), "groups")
	}

	mu      sync.Mutex
	session = func() string { return "" } // reads the session this process runs; see BindSession
	current struct {
		group string
		self  Member
	}
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// ValidName reports whether s can name a group or a member.
func ValidName(s string) bool { return validName.MatchString(s) }

func groupDir(g string) string         { return filepath.Join(root(), g) }
func memberFile(g, name string) string { return filepath.Join(groupDir(g), name+".json") }
func inboxDir(g, name string) string   { return filepath.Join(groupDir(g), name+".inbox") }
func isDataFile(name string) bool {
	return strings.HasSuffix(name, ".json") && !strings.HasPrefix(name, ".")
}
func invalidName(kind, s string) error {
	return fmt.Errorf("invalid %s name %q: use letters, digits, - or _", kind, s)
}
func setCurrent(g string, self Member) { mu.Lock(); current.group, current.self = g, self; mu.Unlock() }

// readJSON decodes path into v; unlike atomicfile.ReadJSON, a missing file is
// an error, which is how a removed member or message shows.
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// BindSession tells the package where to read the session this process runs,
// so a join is stamped with it and a resume recognises its own member file.
// It is read at each use: a join can come from any goroutine, any time.
func BindSession(id func() string) {
	mu.Lock()
	session = id
	mu.Unlock()
}

func currentSession() string {
	mu.Lock()
	id := session
	mu.Unlock()
	return id()
}

// Current returns the group this process is in and its own entry, or "".
func Current() (string, Member) {
	mu.Lock()
	defer mu.Unlock()
	return current.group, current.self
}

// Groups lists the groups that exist.
func Groups() []string {
	entries, _ := os.ReadDir(root())
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// Members lists group's members, sorted by name. Offline ones are included.
func Members(g string) []Member {
	entries, _ := os.ReadDir(groupDir(g))
	var members []Member
	for _, e := range entries {
		if e.IsDir() || !isDataFile(e.Name()) {
			continue
		}
		var m Member
		if readJSON(filepath.Join(groupDir(g), e.Name()), &m) == nil && m.Name != "" {
			members = append(members, m)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	return members
}

// Stamp is the group directory's modification time: every join, leave and
// member update is a rename inside it, so an unchanged stamp means an
// unchanged roster. ok is false when the group is gone.
func Stamp(g string) (stamp time.Time, ok bool) {
	info, err := os.Stat(groupDir(g))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// Join adds this process's session to group g, creating the group if needed.
// The name is claimed exclusively: a taken name fails rather than overwrites.
func Join(g string, self Member) (Member, error) {
	if !ValidName(g) {
		return Member{}, invalidName("group", g)
	}
	if !ValidName(self.Name) {
		return Member{}, invalidName("member", self.Name)
	}
	if cur, _ := Current(); cur != "" {
		return Member{}, fmt.Errorf("already in group %s; leave it first", cur)
	}
	self.SessionID = currentSession()
	self.PID = os.Getpid()
	self.ProcStart, _ = proc.StartTime(self.PID)
	self.JoinedAt = time.Now()
	self.Cwd = homeRelative(self.Cwd)
	if err := os.MkdirAll(groupDir(g), 0o700); err != nil {
		return Member{}, err
	}
	if err := claim(memberFile(g, self.Name), self); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Member{}, fmt.Errorf("%q is already taken in group %s", self.Name, g)
		}
		return Member{}, err
	}
	if err := os.MkdirAll(inboxDir(g, self.Name), 0o700); err != nil {
		return Member{}, err
	}
	setCurrent(g, self)
	return self, nil
}

// homeRelative writes a path under the home directory as ~/…: members share
// one machine and one home, and the model reads every roster line.
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home); ok && (rest == "" || rest[0] == filepath.Separator) {
		return "~" + rest
	}
	return path
}

// claim publishes path only if nothing is there yet: the member is written to
// a temp file and hard-linked into place, which fails when path exists, so a
// reader never sees a half-written file and two joiners never share a name.
func claim(path string, m Member) error {
	tmp := path + ".claim"
	if err := atomicfile.WriteJSON(tmp, m, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Link(tmp, path)
}

// Reclaim restores a membership when its session resumes: the member file must
// still be this session's, and its process must not be online elsewhere.
func Reclaim(g, name string) (Member, error) {
	var self Member
	if err := readJSON(memberFile(g, name), &self); err != nil {
		if _, ok := Stamp(g); !ok {
			return Member{}, fmt.Errorf("group %s was disbanded", g)
		}
		return Member{}, fmt.Errorf("this session was removed from group %s", g)
	}
	if self.SessionID != currentSession() {
		return Member{}, fmt.Errorf("@%s in group %s now belongs to another session", name, g)
	}
	if self.PID != os.Getpid() && self.Online() {
		return Member{}, ErrOnlineElsewhere
	}
	self.PID = os.Getpid()
	self.ProcStart, _ = proc.StartTime(self.PID)
	if err := atomicfile.WriteJSON(memberFile(g, name), self, 0o600); err != nil {
		return Member{}, err
	}
	_ = os.MkdirAll(inboxDir(g, name), 0o700)
	setCurrent(g, self)
	return self, nil
}

// Release takes this process out of its group without leaving it: the member
// goes offline and keeps its inbox, for when the process exits or switches to
// another session.
func Release() {
	g, self := Current()
	if g == "" {
		return
	}
	stillMember := Check() == Joined
	setCurrent("", Member{})
	if !stillMember {
		return
	}
	self.PID, self.ProcStart, self.State = 0, "", ""
	_ = atomicfile.WriteJSON(memberFile(g, self.Name), self, 0o600)
}

// Leave takes this session out of its group for good. The last member out
// removes the group.
func Leave() error {
	g, self := Current()
	if g == "" {
		return errNotJoined
	}
	setCurrent("", Member{})
	return remove(g, self.Name)
}

// Kick removes another member of this process's group.
func Kick(name string) error {
	g, self := Current()
	if g == "" {
		return errNotJoined
	}
	if name == self.Name {
		return errors.New("that is you; use leave")
	}
	if _, err := os.Stat(memberFile(g, name)); err != nil {
		return fmt.Errorf("no member named %q in group %s", name, g)
	}
	return remove(g, name)
}

func remove(g, name string) error {
	_ = os.Remove(memberFile(g, name))
	_ = os.RemoveAll(inboxDir(g, name))
	if len(Members(g)) == 0 {
		return os.RemoveAll(groupDir(g))
	}
	return nil
}

// Disband removes group g outright; its members notice and leave.
func Disband(g string) error {
	if !ValidName(g) {
		return invalidName("group", g)
	}
	if _, ok := Stamp(g); !ok {
		return fmt.Errorf("no group %s", g)
	}
	if cur, _ := Current(); cur == g {
		setCurrent("", Member{})
	}
	return os.RemoveAll(groupDir(g))
}

// SetHold records in this member's file whether it holds messages.
func SetHold(hold bool) error { return updateSelf(func(self *Member) { self.Hold = hold }) }

// SetState records what this session is doing in its member file.
func SetState(s State) error { return updateSelf(func(self *Member) { self.State = s }) }

func updateSelf(change func(*Member)) error {
	g, self := Current()
	if g == "" {
		return errNotJoined
	}
	change(&self)
	if err := atomicfile.WriteJSON(memberFile(g, self.Name), self, 0o600); err != nil {
		return err
	}
	setCurrent(g, self)
	return nil
}

// Check reports how this process's membership stands on disk, so a member
// that was kicked or whose group was disbanded can notice.
func Check() Status {
	g, self := Current()
	if g == "" {
		return NotJoined
	}
	if _, ok := Stamp(g); !ok {
		return Disbanded
	}
	var onDisk Member
	if readJSON(memberFile(g, self.Name), &onDisk) != nil || onDisk.SessionID != self.SessionID {
		return Removed
	}
	return Joined
}

// Forget drops this process's membership without touching disk, once Check
// has found it gone.
func Forget() { setCurrent("", Member{}) }

// Send writes content into the inbox of the member of this process's group
// named to, and returns that member so the caller can say when it will read it.
func Send(to, content string) (Member, error) {
	g, self := Current()
	if g == "" {
		return Member{}, errNotJoined
	}
	if to == self.Name {
		return Member{}, errors.New("cannot send a message to yourself")
	}
	members := Members(g)
	i := slices.IndexFunc(members, func(m Member) bool { return m.Name == to })
	if i < 0 {
		var names []string
		for _, m := range members {
			if m.Name != self.Name {
				names = append(names, m.Name)
			}
		}
		return Member{}, fmt.Errorf("no member named %q in group %s; members: %s", to, g, strings.Join(names, ", "))
	}
	msg := Message{From: self.Name, To: to, Content: content, SentAt: time.Now()}
	name := fmt.Sprintf("%020d-%s.json", msg.SentAt.UnixNano(), self.Name)
	return members[i], atomicfile.WriteJSON(filepath.Join(inboxDir(g, to), name), msg, 0o600)
}

// Inbox lists this member's waiting messages, oldest first, without deleting
// them: Delivered removes each once it has reached the conversation.
func Inbox() []Message {
	g, self := Current()
	if g == "" {
		return nil
	}
	dir := inboxDir(g, self.Name)
	entries, _ := os.ReadDir(dir) // sorted by name, which is by time
	var msgs []Message
	for _, e := range entries {
		if !isDataFile(e.Name()) {
			continue
		}
		var m Message
		path := filepath.Join(dir, e.Name())
		if readJSON(path, &m) == nil {
			m.File = path
			msgs = append(msgs, m)
		}
	}
	return msgs
}

// Delivered deletes messages that have reached the conversation.
func Delivered(msgs []Message) {
	for _, m := range msgs {
		_ = os.Remove(m.File)
	}
}
