package flow

import (
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestLegacyToolsMetadataIsUnknown(t *testing.T) {
	var n Node
	if err := n.fill([]string{"tools: []"}); err == nil || !strings.Contains(err.Error(), `unknown config key "tools"`) {
		t.Fatalf("legacy key: %v", err)
	}
}

func TestWorkerToolMetadata(t *testing.T) {
	for _, tc := range []struct {
		metadata    string
		allow, deny []string
		invalid     bool
	}{
		{"", nil, nil, false}, {"allow_tools: []", []string{}, nil, false}, {"deny_tools: []", nil, []string{}, false},
		{"allow_tools: [Read, Grep]\ndeny_tools: [Grep]", []string{"Read", "Grep"}, []string{"Grep"}, false},
		{"allow_tools: null", nil, nil, true}, {"deny_tools: [Read, null]", nil, nil, true},
		{"allow_tools: []\nexecutor: main", nil, nil, true}, {"executor: main\ndeny_tools: []", nil, nil, true},
		{"allow_tools: []\nallow_tools: [Read]", nil, nil, true}, {"deny_tools: []\ndeny_tools: []", nil, nil, true},
	} {
		t.Run(tc.metadata, func(t *testing.T) {
			n := Node{}
			err := n.fill(strings.Split(tc.metadata+"\n\ninspect", "\n"))
			if (err != nil) != tc.invalid || !tc.invalid && (!reflect.DeepEqual(n.AllowTools, tc.allow) || !reflect.DeepEqual(n.DenyTools, tc.deny)) {
				t.Fatalf("allow=%#v deny=%#v err=%v", n.AllowTools, n.DenyTools, err)
			}
		})
	}
}

func TestResumeRejectsMissingOrChangedWorkerPolicy(t *testing.T) {
	s := NewService(task.NewSupervisor())
	s.SetExecutor(&scriptedExecutor{})
	s.store = newRunStore(t.TempDir())
	for _, policies := range []map[string]string{nil, {"a": "old"}} {
		r := RunRecord{ID: "changed", Definition: "```mermaid\nflowchart LR\n a\n```\n## a\nallow_tools: []\n\ninspect", WorkerPolicies: policies}
		if err := s.store.Put(r); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ResumeRun(r.ID, "a", 100); err == nil || !strings.Contains(err.Error(), "worker policy") {
			t.Fatalf("resume: %v", err)
		}
	}
}
