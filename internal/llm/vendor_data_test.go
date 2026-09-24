package llm

import (
	"context"
	"encoding/json"
	"flag"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/catalog"
)

// go test ./internal/llm -run TestModelDataSnapshot -update-model-data
// refetches models.dev into the snapshot built into the binary.
var updateModelData = flag.Bool("update-model-data", false, "refetch models.dev into data/models.dev.json")

// TestMain keeps the package's tests off the real ~/.san: the model data is
// read once per process, and a developer's cache or overrides must not leak
// into what the tests see.
func TestMain(m *testing.M) {
	flag.Parse()
	home, err := os.MkdirTemp("", "san-llm-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func TestModelDataSnapshot(t *testing.T) {
	if !*updateModelData {
		t.Skip("run with -update-model-data to refetch the snapshot")
	}
	data, err := fetchModelsDev(context.Background(), modelsDevURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLineups("data/models.dev.json", data); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(b bool) *bool { return &b }

func vendorFor(t *testing.T, id string) catalog.Vendor {
	t.Helper()
	v, ok := catalog.Find(id)
	if !ok {
		t.Fatalf("no catalog vendor %q", id)
	}
	return v
}

func efforts(m ai.Model) string {
	names := make([]string, len(m.Reasoning))
	for i, level := range m.Reasoning {
		names[i] = string(level.Effort)
		if level.Default {
			names[i] += "*"
		}
	}
	return strings.Join(names, ",")
}

// The data names which efforts a model offers; the protocol decides which of
// them it can tell apart; the lineup picks the default. Each case is a real
// model's shape in models.dev.
func TestReasoningLadderIsTheDataCutToTheProtocol(t *testing.T) {
	effort := func(values ...string) reasoningOption { return reasoningOption{Type: "effort", Values: values} }
	toggle := reasoningOption{Type: "toggle"}
	budget := reasoningOption{Type: "budget_tokens"}

	cases := []struct {
		name, vendor, id string
		lineup           lineup
		spec             modelSpec
		known            bool
		want             string
	}{
		{name: "Claude adaptive offers off and every named level",
			vendor: "anthropic", id: "claude-opus-5", lineup: lineup{DefaultEffort: ai.EffortHigh}, known: true,
			spec: modelSpec{Reasoning: boolPtr(true), ReasoningOptions: []reasoningOption{effort("low", "medium", "high", "xhigh", "max")}},
			want: "off,low,medium,high*,xhigh,max"},
		{name: "a model that always reasons has no off rung",
			vendor: "anthropic", id: "claude-fable-5", lineup: lineup{DefaultEffort: ai.EffortHigh}, known: true,
			spec: modelSpec{Reasoning: boolPtr(true), AlwaysReasons: true, ReasoningOptions: []reasoningOption{effort("low", "medium", "high", "xhigh", "max")}},
			want: "low,medium,high*,xhigh,max"},
		{name: "Responses can only say off when the model lists none",
			vendor: "openai", id: "gpt-5.5", lineup: lineup{DefaultEffort: ai.EffortMedium}, known: true,
			spec: modelSpec{Reasoning: boolPtr(true), ReasoningOptions: []reasoningOption{effort("none", "low", "medium", "high", "xhigh")}},
			want: "off,low,medium*,high,xhigh"},
		{name: "Responses without none offers no off",
			vendor: "openai", id: "gpt-5", lineup: lineup{DefaultEffort: ai.EffortMedium}, known: true,
			spec: modelSpec{Reasoning: boolPtr(true), ReasoningOptions: []reasoningOption{effort("minimal", "low", "medium", "high")}},
			want: "low,medium*,high"},
		{name: "a model that does not reason has no ladder",
			vendor: "openai", id: "gpt-4o", known: true,
			spec: modelSpec{Reasoning: boolPtr(false)},
			want: ""},
		{name: "an on/off protocol collapses named levels onto on",
			vendor: "moonshot", id: "kimi-k3", known: true,
			spec: modelSpec{Reasoning: boolPtr(true), ReasoningOptions: []reasoningOption{toggle, effort("low", "high", "max")}},
			want: "off*,high"},
		{name: "a budget protocol keeps the levels it has budgets for",
			vendor: "volcengine", id: "doubao-seed-2-0-pro-260215", known: true,
			spec: modelSpec{Reasoning: boolPtr(true), ReasoningOptions: []reasoningOption{effort("none", "minimal", "low", "medium", "high", "xhigh", "max")}},
			want: "off*,low,medium,high"},
		{name: "a toggle alone offers the protocol's whole ladder",
			vendor: "alibaba", id: "qwen3.7-plus", known: true,
			spec: modelSpec{Reasoning: boolPtr(true), ReasoningOptions: []reasoningOption{toggle, budget}},
			want: "off*,low,medium,high"},
		{name: "an unlisted model inherits the protocol's ladder",
			vendor: "anthropic", id: "claude-opus-9", lineup: lineup{DefaultEffort: ai.EffortHigh},
			want: "off,low,medium,high*,xhigh,max"},
		{name: "an endpoint with no reasoning field offers nothing",
			vendor: "copilot", id: "gpt-5.4", known: true,
			spec: modelSpec{Reasoning: boolPtr(true), ReasoningOptions: []reasoningOption{effort("low", "high")}},
			want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.lineup.apply(vendorFor(t, tc.vendor).Model(tc.id), tc.spec, tc.known)
			if got := efforts(m); got != tc.want {
				t.Errorf("ladder = %q, want %q", got, tc.want)
			}
		})
	}
}

// Two protocol differences are per model, and the data states both: Claude
// 4.6 still takes a temperature, and Gemini 2.5 takes a budget where Gemini 3
// takes a level.
func TestTheDataAdjustsTheProtocolPerModel(t *testing.T) {
	claude46 := lineup{}.apply(vendorFor(t, "anthropic").Model("claude-opus-4-6"), modelSpec{
		Temperature:      boolPtr(true),
		ReasoningOptions: []reasoningOption{{Type: "effort", Values: []string{"low", "high"}}, {Type: "budget_tokens"}},
	}, true)
	if c := ai.CompatOf[ai.AnthropicCompat](claude46); c.NoTemperature || !c.ForceAdaptiveThinking {
		t.Errorf("Claude 4.6 compat = %+v, want adaptive thinking with temperature allowed", c)
	}

	gemini25 := lineup{}.apply(vendorFor(t, "google").Model("gemini-2.5-pro"), modelSpec{
		ReasoningOptions: []reasoningOption{{Type: "budget_tokens"}},
	}, true)
	if ai.CompatOf[ai.GoogleCompat](gemini25).ThinkingLevel {
		t.Error("Gemini 2.5 was sent a thinking level; it takes a budget")
	}
	// The derived wire value follows: a budget, not a level.
	if level, ok := gemini25.ResolveLevel(ai.EffortHigh); !ok || level.Budget == 0 || level.Value != "" {
		t.Errorf("Gemini 2.5 high = %+v, want a budget", level)
	}
}

func TestAModelReadsItsLimitsPricesAndInputFromTheData(t *testing.T) {
	l := lineup{DefaultInput: []string{"text", "image"}, DefaultLimit: &specLimit{Context: 1_000_000, Output: 128_000}}
	m := l.apply(vendorFor(t, "minimax").Model("MiniMax-M3"), modelSpec{
		Name:       "MiniMax M3",
		Limit:      &specLimit{Context: 1_048_576, Output: 512_000},
		Modalities: &specModalities{Input: []string{"text", "image", "video"}},
		Cost: &specCost{Input: 0.3, Output: 1.2, CacheRead: 0.06, Tiers: []specTier{{
			Input: 0.6, Output: 2.4, CacheRead: 0.12,
			Tier: struct {
				Type string `json:"type"`
				Size int    `json:"size"`
			}{Type: "context", Size: 512_000},
		}}},
	}, true)
	if m.Name != "MiniMax M3" || m.ContextWindow != 1_048_576 || m.MaxOutput != 512_000 {
		t.Errorf("got %q %d/%d", m.Name, m.ContextWindow, m.MaxOutput)
	}
	if !m.Accepts(ai.ModalityVideo) {
		t.Error("the model's own input list was not used")
	}
	if m.Pricing.Currency != ai.USD || len(m.Pricing.Tiers) != 1 || m.Pricing.Tiers[0].AboveInputTokens != 512_000 {
		t.Errorf("pricing = %+v", m.Pricing)
	}

	unlisted := l.apply(vendorFor(t, "minimax").Model("MiniMax-M9"), modelSpec{}, false)
	if unlisted.ContextWindow != 1_000_000 || !unlisted.Accepts(ai.ModalityImage) {
		t.Errorf("an unlisted model did not inherit the lineup's defaults: %d, %v", unlisted.ContextWindow, unlisted.Input)
	}
}

func TestLaterLayersOverrideFieldByField(t *testing.T) {
	data := lineups{"deepseek": {Models: map[string]modelSpec{
		"deepseek-v4-pro": {Name: "DeepSeek V4 Pro", Limit: &specLimit{Context: 1_000_000, Output: 384_000}, Cost: &specCost{Input: 0.435}},
	}}}
	data.overlay(lineups{"deepseek": {DefaultEffort: ai.EffortHigh, Models: map[string]modelSpec{
		"DeepSeek-V4-Pro": {Limit: &specLimit{Context: 512_000}, Cost: &specCost{Input: 1.32}},
		"deepseek-v5":     {Name: "DeepSeek V5"},
	}}})

	got := data["deepseek"]
	pro := got.Models["deepseek-v4-pro"]
	if pro.Name != "DeepSeek V4 Pro" || pro.Limit.Context != 512_000 || pro.Limit.Output != 384_000 || pro.Cost.Input != 1.32 {
		t.Errorf("merged = %+v %+v %+v", pro, *pro.Limit, *pro.Cost)
	}
	if _, ok := got.Models["deepseek-v5"]; !ok || got.DefaultEffort != ai.EffortHigh {
		t.Error("the overlay's new model or default was dropped")
	}
}

// What comes off the network can describe a model and nothing else: it is
// re-keyed by San's vendors, cut to agent-usable models, and loses every field
// San does not read — an endpoint most of all.
func TestModelsDevIsTrimmedToWhatSanReads(t *testing.T) {
	raw := `{
	  "xiaomi": {"api": "https://evil.example/v1", "env": ["MIMO_API_KEY"], "models": {
	    "mimo-v2.5-pro": {"name": "MiMo", "tool_call": true, "limit": {"context": 1048576, "output": 131072},
	                      "cost": {"input": 0.4, "output": 0.8}, "provider": {"api": "https://evil.example"}},
	    "mimo-tts": {"tool_call": false},
	    "mimo-huge": {"tool_call": true, "limit": {"context": 999999999999}, "cost": {"input": -1}}
	  }},
	  "google-vertex": {"models": {
	    "gemini-3.5-flash": {"tool_call": true},
	    "claude-opus-4-8@default": {"tool_call": true}
	  }},
	  "google-vertex-anthropic": {"models": {"claude-opus-4-8@default": {"tool_call": true}}},
	  "unrelated": {"models": {"x": {"tool_call": true}}}
	}`
	data, err := trimModelsDev([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(data["mimo"].Models)); !slices.Equal(got, []string{"mimo-huge", "mimo-v2.5-pro"}) {
		t.Errorf("mimo models = %v", got)
	}
	if huge := data["mimo"].Models["mimo-huge"]; huge.Limit != nil || huge.Cost != nil {
		t.Errorf("implausible figures survived: %+v", huge)
	}
	if got := slices.Sorted(maps.Keys(data["google-vertex"].Models)); !slices.Equal(got, []string{"gemini-3.5-flash"}) {
		t.Errorf("google-vertex kept another publisher's model: %v", got)
	}
	if _, ok := data["anthropic-vertex"].Models["claude-opus-4-8"]; !ok {
		t.Error("the @default alias was not reduced to the dateless ID")
	}
	out, _ := json.Marshal(data)
	if strings.Contains(string(out), "evil.example") || strings.Contains(string(out), "MIMO_API_KEY") {
		t.Errorf("an endpoint or credential name survived the trim: %s", out)
	}
}

// The same models cost different amounts depending on the way in, and a flat
// subscription costs nothing per token.
func TestCostIsPricedPerAuthMethod(t *testing.T) {
	cases := []struct {
		provider   ProviderID
		authMethod AuthMethod
		model      string
		priced     bool
	}{
		{Anthropic, AuthAPIKey, "claude-opus-5", true},
		{OpenAI, AuthAPIKey, "gpt-5.5", true},
		{OpenAI, AuthSubscription, "gpt-5.5", false},
		{BigModel, AuthCoding, "glm-5", false},
	}
	for _, tc := range cases {
		_, ok := EstimateCost(tc.provider, tc.authMethod, tc.model, Usage{Input: 1_000_000})
		if ok != tc.priced {
			t.Errorf("%s:%s %s priced = %v, want %v", tc.provider, tc.authMethod, tc.model, ok, tc.priced)
		}
	}
}

// Every vendor the data names is one the catalog knows, so a typo in a key
// cannot silently leave a vendor without its data.
func TestEmbeddedDataNamesRealVendors(t *testing.T) {
	for _, name := range []string{"data/models.dev.json", "data/san.json"} {
		for vendorID := range embeddedLineups(name) {
			if vendorID == string(CustomProvider) {
				continue
			}
			if _, ok := catalog.Find(vendorID); !ok {
				t.Errorf("%s names %q, which the catalog does not know", name, vendorID)
			}
		}
	}
	snapshot := embeddedLineups("data/models.dev.json")
	for vendorID := range modelsDevSources {
		if len(snapshot[vendorID].Models) == 0 {
			t.Errorf("the snapshot has no models for %s", vendorID)
		}
	}
}

func TestAStaleCacheIsRefreshedAndAFreshOneIsNot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := modelDataCachePath()
	if err := writeLineups(path, lineups{"deepseek": {}}); err != nil {
		t.Fatal(err)
	}
	// Fresh: no fetch is attempted, so an unreachable network cannot fail it.
	if err := RefreshModelData(context.Background()); err != nil {
		t.Fatalf("a fresh cache was refetched: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
