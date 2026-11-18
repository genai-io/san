package setting

import "testing"

func TestGitMetadataWritesAreNotReadOnly(t *testing.T) {
	for _, prefix := range []string{"git ", "git -C /tmp/repo ", "git --git-dir=/tmp/repo/.git "} {
		for _, args := range []string{
			"symbolic-ref refs/heads/review-alias refs/heads/main",
			"symbolic-ref HEAD refs/heads/other",
			"symbolic-ref --delete refs/heads/review-alias",
			"symbolic-ref -m reason HEAD refs/heads/other",
			"reflog expire --expire=all refs/heads/main",
			"reflog delete HEAD@{0}",
			"reflog drop refs/heads/main",
			"reflog write refs/heads/main old new message",
		} {
			if IsReadOnlyBashCommand(prefix + args) {
				t.Errorf("metadata write classified as read-only: %s%s", prefix, args)
			}
		}
		for _, args := range []string{
			"symbolic-ref HEAD", "symbolic-ref --quiet --short HEAD",
			"reflog", "reflog show HEAD", "reflog exists refs/heads/main", "reflog list",
		} {
			if !IsReadOnlyBashCommand(prefix + args) {
				t.Errorf("metadata query rejected: %s%s", prefix, args)
			}
		}
	}
}
