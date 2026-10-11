package subagent

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestEditShellConstraintsDiscloseParentValidation(t *testing.T) {
	constraints := effectiveToolConstraints(&AgentConfig{}, PermissionAcceptEdits, []core.ToolSchema{{Name: "Bash"}, {Name: "Read"}})
	if len(constraints) != 1 {
		t.Fatalf("expected shell constraint only, got %v", constraints)
	}
	for _, phrase := range []string{"unless explicitly allowed", "tests/build require an explicit allowed command or parent validation", "Do not retry denied commands"} {
		if !strings.Contains(constraints[0], phrase) {
			t.Errorf("constraint missing %q: %s", phrase, constraints[0])
		}
	}
}
