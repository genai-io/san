package fs

import (
	"context"
	"slices"
	"sync"
	"testing"
)

func TestShellEnvProviderLazyAndDynamic(t *testing.T) {
	type rootKey struct{}
	calls := 0
	value := "first"
	ctx := WithShellEnvProvider(context.Background(), func(ctx context.Context) []string {
		calls++
		return []string{"SAN_TEST_OWNER=" + value, "SAN_TEST_ROOT=" + ctx.Value(rootKey{}).(string)}
	})
	if calls != 0 {
		t.Fatal("provider evaluated before shell execution")
	}
	for _, root := range []string{"one", "two"} {
		value = root
		env := shellEnv(context.WithValue(ctx, rootKey{}, root))
		want := []string{"SAN_TEST_OWNER=" + root, "SAN_TEST_ROOT=" + root}
		if !slices.Equal(env[len(env)-len(want):], want) {
			t.Fatalf("scoped environment must be appended in provider order: %v", env)
		}
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}
}

func TestShellEnvScopedOverrides(t *testing.T) {
	previous, _ := extraEnvProvider.Load().(func(context.Context) []string)
	t.Cleanup(func() { SetEnvProvider(previous) })
	globalCalls := 0
	SetEnvProvider(func(context.Context) []string {
		globalCalls++
		return []string{"SAN_TEST_GLOBAL=leak"}
	})
	base := context.Background()
	for name, ctx := range map[string]context.Context{
		"empty slice":         WithShellEnv(base, nil),
		"empty provider":      WithShellEnvProvider(base, func(context.Context) []string { return nil }),
		"nil provider":        WithShellEnvProvider(base, nil),
		"provider over slice": WithShellEnvProvider(WithShellEnv(base, []string{"SAN_TEST_GLOBAL=leak"}), func(context.Context) []string { return nil }),
		"slice over provider": WithShellEnv(WithShellEnvProvider(base, func(context.Context) []string { t.Fatal("shadowed provider called"); return nil }), nil),
	} {
		t.Run(name, func(t *testing.T) {
			if slices.Contains(shellEnv(ctx), "SAN_TEST_GLOBAL=leak") {
				t.Fatal("scoped environment leaked global or shadowed environment")
			}
		})
	}
	if globalCalls != 0 {
		t.Fatalf("global provider called %d times for scoped contexts", globalCalls)
	}
	if !slices.Contains(shellEnv(base), "SAN_TEST_GLOBAL=leak") || globalCalls != 1 {
		t.Fatal("unscoped context did not retain global provider")
	}
}

func TestShellEnvSnapshotAndConcurrentOwners(t *testing.T) {
	input := []string{"SAN_TEST_OWNER=snapshot"}
	snapshot := WithShellEnv(context.Background(), input)
	input[0] = "SAN_TEST_OWNER=changed"
	if !slices.Contains(shellEnv(snapshot), "SAN_TEST_OWNER=snapshot") {
		t.Fatal("WithShellEnv no longer copies its input")
	}
	var wg sync.WaitGroup
	for _, owner := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := WithShellEnvProvider(context.Background(), func(context.Context) []string {
				return []string{"SAN_TEST_OWNER=" + owner}
			})
			for range 20 {
				env := shellEnv(ctx)
				if env[len(env)-1] != "SAN_TEST_OWNER="+owner {
					t.Error("shell environment crossed owners")
				}
			}
		}()
	}
	wg.Wait()
}
