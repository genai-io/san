package plugin

import (
	"context"
	"reflect"
	"testing"
)

func TestScopedEnvironmentDoesNotUseGlobalPluginsOrRoot(t *testing.T) {
	first, second := NewRegistry(), NewRegistry()
	first.plugins["first"] = &Plugin{Path: "/first", Enabled: true}
	second.plugins["second"] = &Plugin{Path: "/second", Enabled: true}
	// Empty session must not inherit the process-wide active plugin fallback.
	SetRootProvider(func() string { return "/global" })
	t.Cleanup(func() { SetRootProvider(nil) })
	empty := WithRegistry(context.Background(), NewRegistry())
	if got := PluginEnv(empty); len(got) != 0 {
		t.Fatalf("global environment leak: %v", got)
	}
	a := PluginEnv(WithRegistry(context.Background(), first))
	b := PluginEnv(WithRegistry(context.Background(), second))
	if reflect.DeepEqual(a, b) {
		t.Fatalf("registries share environment: %v", a)
	}
	if root := activePluginRoot(empty); root != "" {
		t.Fatalf("global root leak: %s", root)
	}
}
