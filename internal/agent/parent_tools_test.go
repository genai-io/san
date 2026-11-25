package agent

import (
	"slices"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
)

func TestParentToolSourceUsesOwnerCatalogue(t *testing.T) {
	var runtime Runtime
	startup := []core.ToolSchema{{Name: "startup-only"}}
	tools := runtime.ParentToolSource(nil, startup)
	startup[0].Name = "changed"
	tools()[0] = "changed"
	if got := tools(); !slices.Equal(got, []string{"startup-only"}) {
		t.Fatalf("startup catalogue was not isolated: %v", got)
	}
	other := runtime.ParentToolSource(new(session.Session), startup)
	if err := runtime.Start(BuildParams{Provider: stubProvider{}, ModelID: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, &runtime)
	got := tools()
	if slices.Contains(got, "startup-only") || len(got) != len(runtime.Tools().Schemas()) {
		t.Fatalf("expected live owner catalogue, got %v", got)
	}
	if got := other(); got == nil || len(got) != 0 {
		t.Fatalf("foreign runtime supplied a tool catalogue: %v", got)
	}
}
