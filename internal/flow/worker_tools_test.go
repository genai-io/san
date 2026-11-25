package flow

import (
	"slices"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestWorkerToolMetadata(t *testing.T) {
	for _, tc := range []struct {
		metadata string
		want     []string
		invalid  bool
	}{
		{"", nil, false}, {"tools: []", []string{}, false}, {"tools: [Read, Grep]", []string{"Read", "Grep"}, false},
		{"tools: null", nil, true}, {"tools: [Read, null]", nil, true}, {"tools: []\nexecutor: main", nil, true}, {"tools: []\ntools: [Read]", nil, true},
	} {
		t.Run(tc.metadata, func(t *testing.T) {
			n := Node{}
			err := n.fill(strings.Split(tc.metadata+"\n\ninspect", "\n"))
			if (err != nil) != tc.invalid || !tc.invalid && (!slices.Equal(n.Tools, tc.want) || (n.Tools == nil) != (tc.want == nil)) {
				t.Fatalf("tools=%#v err=%v", n.Tools, err)
			}
		})
	}
}

func TestResumeRejectsMissingOrChangedWorkerPolicy(t *testing.T) {
	s := NewService(task.NewManager())
	s.SetExecutor(&scriptedExecutor{})
	s.store = NewStore(t.TempDir())
	for _, policies := range []map[string]string{nil, {"a": "old"}} {
		r := RunRecord{ID: "changed", Definition: "```mermaid\nflowchart LR\n a\n```\n## a\ntools: []\n\ninspect", WorkerPolicies: policies}
		if err := s.store.Put(r); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ResumeRun(r.ID, "a", 100); err == nil || !strings.Contains(err.Error(), "worker policy") {
			t.Fatalf("resume: %v", err)
		}
	}
}
