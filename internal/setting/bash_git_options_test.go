package setting

import "testing"

func TestReadOnlyGitGlobalOptions(t *testing.T) {
	for _, command := range []string{
		"git -C /tmp/review rev-parse HEAD",
		"git -C '/tmp/review with spaces' --no-pager diff --stat",
		"git -C /tmp -C child status --short",
		"git -C/tmp/review log -1",
		"git --git-dir /tmp/repo.git --work-tree /tmp/repo show HEAD",
		"git --git-dir=/tmp/repo.git --work-tree=/tmp/repo --no-optional-locks status",
	} {
		t.Run(command, func(t *testing.T) {
			if !IsReadOnlyBashCommand(command) {
				t.Fatal("genuine read rejected")
			}
		})
	}
	for _, command := range []string{
		"git -C", "git --git-dir", "git --unknown status",
		"git -C /tmp reset --hard", "git -C /tmp clean -fd",
		"git -C /tmp checkout branch", "git -C /tmp push --force",
		"git -C /tmp diff --output=/tmp/output",
		"git -C /tmp diff --ext-diff", "git -C /tmp log --textconv",
		"git -c core.pager=evil log", "git -c diff.external=evil diff",
		"git --config-env=core.pager=PAGER log", "git --exec-path=/tmp status",
		"git -C status reset --hard", "git --work-tree status clean -fd",
	} {
		t.Run(command, func(t *testing.T) {
			if IsReadOnlyBashCommand(command) {
				t.Fatal("write, unsafe option, or incomplete command admitted")
			}
		})
	}
}
