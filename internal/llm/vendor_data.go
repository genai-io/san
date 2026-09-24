package llm

import (
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/catalog"
	sdkprovider "github.com/genai-io/sdk-go/pkg/ai/provider"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/log"
)

// What San knows about a vendor's models, as opposed to how to reach them.
//
// The SDK's catalog says how to reach a vendor and speak its protocol. Which
// models it serves, their limits and prices, what they accept and which
// reasoning efforts they offer change every few weeks, so they are data San
// keeps, in layers, each overriding the one before it:
//
//	data/models.dev.json    a snapshot of models.dev, built into the binary
//	~/.san/cache/models.json  the same, refreshed daily (RefreshModelData)
//	data/san.json           what models.dev lacks or gets wrong, kept by hand
//	~/.san/models.json      the user's own corrections
//
// and the endpoint's live listing over all of them. The shape is models.dev's,
// keyed by catalog vendor ID. It carries no endpoint, header or credential:
// data fetched from the network can describe a model, never redirect a key.

//go:embed data/models.dev.json data/san.json
var embeddedModelData embed.FS

// lineups is every vendor's lineup, keyed by catalog vendor ID.
type lineups map[string]lineup

// lineup is one vendor's models, plus what a model the data does not list
// inherits.
type lineup struct {
	// DefaultEffort is the reasoning rung a model starts on. Empty means off,
	// or the lowest rung for a model that cannot turn reasoning off.
	DefaultEffort ai.Effort `json:"default_effort,omitempty"`
	// DefaultInput and DefaultLimit describe a model the data does not list —
	// usually one newer than the data.
	DefaultInput []string   `json:"default_input,omitempty"`
	DefaultLimit *specLimit `json:"default_limit,omitempty"`

	Models map[string]modelSpec `json:"models,omitempty"`
}

// modelSpec is one model, in models.dev's field names.
type modelSpec struct {
	Name             string            `json:"name,omitempty"`
	ReleaseDate      string            `json:"release_date,omitempty"`
	ToolCall         *bool             `json:"tool_call,omitempty"`
	Reasoning        *bool             `json:"reasoning,omitempty"`
	ReasoningOptions []reasoningOption `json:"reasoning_options,omitempty"`
	Temperature      *bool             `json:"temperature,omitempty"`
	Modalities       *specModalities   `json:"modalities,omitempty"`
	Limit            *specLimit        `json:"limit,omitempty"`
	Cost             *specCost         `json:"cost,omitempty"`
	Status           string            `json:"status,omitempty"`

	// San's own fields, for what models.dev cannot say.
	Replacement string `json:"replacement,omitempty"`
	// AlwaysReasons marks a model that rejects a request to stop reasoning,
	// so it offers no off rung. Claude Fable 5 is one.
	AlwaysReasons bool `json:"always_reasons,omitempty"`
}

// reasoningOption is one way a model's reasoning can be controlled: "toggle"
// (on or off), "effort" (named levels, in Values) or "budget_tokens".
type reasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
}

type specModalities struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

type specLimit struct {
	Context int `json:"context,omitempty"`
	Output  int `json:"output,omitempty"`
}

// specCost is a rate card per million tokens. models.dev states USD.
type specCost struct {
	Currency   string     `json:"currency,omitempty"`
	Input      float64    `json:"input,omitempty"`
	Output     float64    `json:"output,omitempty"`
	CacheRead  float64    `json:"cache_read,omitempty"`
	CacheWrite float64    `json:"cache_write,omitempty"`
	Tiers      []specTier `json:"tiers,omitempty"`
}

// specTier is a rate card that takes over past a prompt size.
type specTier struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Tier       struct {
		Type string `json:"type"`
		Size int    `json:"size"`
	} `json:"tier"`
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

var (
	modelDataOnce sync.Once
	modelData     lineups
)

// loadedModelData returns the layered lineups, read once per process. A
// refresh lands in the cache and takes effect on the next start.
func loadedModelData() lineups {
	modelDataOnce.Do(func() { modelData = readModelData() })
	return modelData
}

func readModelData() lineups {
	data := embeddedLineups("data/models.dev.json")
	// A fetched copy replaces the snapshot vendor by vendor, so a vendor the
	// fetch lost keeps what the build shipped with.
	if cached, err := readLineupsFile(modelDataCachePath()); err == nil {
		maps.Copy(data, cached)
	} else if !os.IsNotExist(err) {
		log.Logger().Warn("ignoring the cached model data: " + err.Error())
	}
	data.overlay(embeddedLineups("data/san.json"))
	if user, err := readLineupsFile(userModelDataPath()); err == nil {
		data.overlay(user)
	} else if !os.IsNotExist(err) {
		log.Logger().Warn("ignoring ~/.san/models.json: " + err.Error())
	}
	return data
}

// embeddedLineups reads a file built into the binary. It cannot be missing or
// malformed without a test failing first, so an error here is a build defect.
func embeddedLineups(name string) lineups {
	raw, err := embeddedModelData.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("llm: embedded %s: %v", name, err))
	}
	var data lineups
	if err := json.Unmarshal(raw, &data); err != nil {
		panic(fmt.Sprintf("llm: embedded %s: %v", name, err))
	}
	return data
}

func readLineupsFile(path string) (lineups, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data lineups
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return data, nil
}

// userModelDataPath is where a user corrects or extends the data by hand.
func userModelDataPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(confdir.Dir(home), "models.json")
}

// overlay layers over onto data, field by field: a later layer states only
// what it changes.
func (data lineups) overlay(over lineups) {
	for vendorID, top := range over {
		base := data[vendorID]
		if top.DefaultEffort != "" {
			base.DefaultEffort = top.DefaultEffort
		}
		if top.DefaultInput != nil {
			base.DefaultInput = top.DefaultInput
		}
		if top.DefaultLimit != nil {
			base.DefaultLimit = top.DefaultLimit.over(base.DefaultLimit)
		}
		if len(top.Models) > 0 && base.Models == nil {
			base.Models = make(map[string]modelSpec, len(top.Models))
		}
		for id, spec := range top.Models {
			id = strings.ToLower(id)
			base.Models[id] = base.Models[id].overlay(spec)
		}
		data[vendorID] = base
	}
}

func (s modelSpec) overlay(top modelSpec) modelSpec {
	if top.Name != "" {
		s.Name = top.Name
	}
	if top.ReleaseDate != "" {
		s.ReleaseDate = top.ReleaseDate
	}
	s.ToolCall = cmp.Or(top.ToolCall, s.ToolCall)
	s.Reasoning = cmp.Or(top.Reasoning, s.Reasoning)
	s.Temperature = cmp.Or(top.Temperature, s.Temperature)
	if top.ReasoningOptions != nil {
		s.ReasoningOptions = top.ReasoningOptions
	}
	if top.Modalities != nil {
		s.Modalities = top.Modalities
	}
	if top.Limit != nil {
		s.Limit = top.Limit.over(s.Limit)
	}
	if top.Cost != nil {
		s.Cost = top.Cost
	}
	if top.Status != "" {
		s.Status = top.Status
	}
	if top.Replacement != "" {
		s.Replacement = top.Replacement
	}
	s.AlwaysReasons = s.AlwaysReasons || top.AlwaysReasons
	return s
}

// over keeps the base's figure wherever the top states none.
func (top *specLimit) over(base *specLimit) *specLimit {
	out := specLimit{}
	if base != nil {
		out = *base
	}
	out.Context = cmp.Or(top.Context, out.Context)
	out.Output = cmp.Or(top.Output, out.Output)
	return &out
}

// ---------------------------------------------------------------------------
// Resolving a model
// ---------------------------------------------------------------------------

// vendorModels answers for one vendor's models: how to reach them from the
// catalog row, what they are from the lineup.
type vendorModels struct {
	vendor catalog.Vendor
	lineup lineup
}

func newVendorModels(vendor catalog.Vendor) vendorModels {
	return vendorModels{vendor: vendor, lineup: loadedModelData()[vendor.ID]}
}

// model resolves one ID, listed in the data or not.
func (v vendorModels) model(id string) ai.Model {
	spec, known := v.lineup.find(id)
	return v.lineup.apply(v.vendor.Model(id), spec, known)
}

// resolve fills in a model a live listing reported, keeping every figure the
// listing did state.
func (v vendorModels) resolve(live ai.Model) ai.Model {
	return sdkprovider.MergeListing(v.model(live.ID), live)
}

// list is the vendor's lineup as a picker baseline, newest first: the models
// an agent can drive — they call tools and answer in text.
func (v vendorModels) list() []ai.Model {
	ids := make([]string, 0, len(v.lineup.Models))
	for id, spec := range v.lineup.Models {
		if spec.agentUsable() {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b string) int {
		return cmp.Or(
			strings.Compare(v.lineup.Models[b].ReleaseDate, v.lineup.Models[a].ReleaseDate),
			strings.Compare(a, b))
	})
	out := make([]ai.Model, len(ids))
	for i, id := range ids {
		out[i] = v.model(id)
	}
	return out
}

func (s modelSpec) agentUsable() bool {
	if s.ToolCall != nil && !*s.ToolCall {
		return false
	}
	return s.Modalities == nil || len(s.Modalities.Output) == 0 || slices.Contains(s.Modalities.Output, "text")
}

// find looks an ID up the way endpoints spell it: in any case, and with or
// without the vendor prefix some listings add ("xiaomi/mimo-v2.5-pro").
func (l lineup) find(id string) (modelSpec, bool) {
	id = strings.ToLower(id)
	if spec, ok := l.Models[id]; ok {
		return spec, true
	}
	if _, bare, ok := strings.Cut(id, "/"); ok {
		spec, found := l.Models[bare]
		return spec, found
	}
	return modelSpec{}, false
}

// apply fills a protocol-stamped model with what the data says about it.
func (l lineup) apply(m ai.Model, spec modelSpec, known bool) ai.Model {
	if spec.Name != "" {
		m.Name = spec.Name
	}
	if limit := cmp.Or(spec.Limit, l.DefaultLimit); limit != nil {
		m.ContextWindow, m.MaxOutput = limit.Context, limit.Output
	}
	input := l.DefaultInput
	if spec.Modalities != nil && spec.Modalities.Input != nil {
		input = spec.Modalities.Input
	}
	m.Input = modalities(input)
	if spec.Cost != nil {
		m.Pricing = spec.Cost.pricing()
	}
	switch spec.Status {
	case "beta", "alpha", "preview":
		m.Stage = ai.StagePreview
	case "deprecated":
		m.Stage = ai.StageDeprecated
	case "retired":
		m.Stage = ai.StageRetired
	}
	m.Replacement = spec.Replacement
	m.Compat = spec.adjustCompat(m.Compat)
	m.Reasoning = l.ladder(m, spec, known)
	return m
}

// modalities keeps the input kinds the SDK knows by name. Nil stays nil: text
// only.
func modalities(names []string) []ai.Modality {
	var out []ai.Modality
	for _, name := range names {
		switch kind := ai.Modality(name); kind {
		case ai.ModalityText, ai.ModalityImage, ai.ModalityAudio, ai.ModalityVideo, ai.ModalityPDF:
			out = append(out, kind)
		}
	}
	return out
}

func (c specCost) pricing() ai.Pricing {
	p := ai.Pricing{
		Currency: cmp.Or(c.Currency, ai.USD),
		Input:    c.Input, Output: c.Output, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite,
	}
	for _, t := range c.Tiers {
		if t.Tier.Type != "context" || t.Tier.Size <= 0 {
			continue
		}
		p.Tiers = append(p.Tiers, ai.PricingTier{
			AboveInputTokens: t.Tier.Size,
			Input:            t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite,
		})
	}
	return p
}

// adjustCompat turns the vendor's protocol behavior into this model's, for
// the two differences the data states: a model that rejects temperature, and
// a model whose reasoning takes a token budget rather than a named effort.
// It never switches adaptive thinking or thinking levels on — whether an
// endpoint speaks them is the vendor row's to say.
func (s modelSpec) adjustCompat(compat any) any {
	switch c := compat.(type) {
	case ai.AnthropicCompat:
		if s.Temperature != nil {
			c.NoTemperature = !*s.Temperature
		}
		if c.ForceAdaptiveThinking && s.hasOption("budget_tokens") && !s.hasOption("effort") {
			c.ForceAdaptiveThinking = false
		}
		return c
	case ai.GoogleCompat:
		if c.ThinkingLevel && s.hasOption("budget_tokens") && !s.hasOption("effort") {
			c.ThinkingLevel = false
		}
		return c
	}
	return compat
}

func (s modelSpec) hasOption(kind string) bool {
	return slices.ContainsFunc(s.ReasoningOptions, func(o reasoningOption) bool { return o.Type == kind })
}

// ladder is the reasoning rungs the model offers: what the data names, cut to
// what the protocol can tell apart, with the lineup's default marked. The SDK
// derives each rung's wire value; the data only says which rungs exist.
func (l lineup) ladder(m ai.Model, spec modelSpec, known bool) []ai.ReasoningLevel {
	wire := m.WireEfforts()
	if len(wire) == 0 || (known && spec.Reasoning != nil && !*spec.Reasoning) {
		return nil
	}

	offered := wire
	if named, off := spec.namedEfforts(); known && len(named) > 0 {
		// Only the Responses protocol has to spell "off" ("none"), and a model
		// that does not list it rejects it; elsewhere off is an omission.
		off = off || m.API != ai.APIOpenAIResponses
		offered = slices.DeleteFunc(slices.Clone(wire), func(e ai.Effort) bool {
			if e == ai.EffortOff {
				return !off
			}
			return !slices.Contains(named, e)
		})
		// A protocol coarser than the model's levels (on/off only) still has
		// to offer "on".
		if !slices.ContainsFunc(offered, func(e ai.Effort) bool { return e != ai.EffortOff }) {
			offered = append(offered, wire[len(wire)-1])
		}
	}
	if spec.AlwaysReasons {
		offered = slices.DeleteFunc(slices.Clone(offered), func(e ai.Effort) bool { return e == ai.EffortOff })
	}

	levels := make([]ai.ReasoningLevel, len(offered))
	for i, effort := range offered {
		levels[i] = ai.ReasoningLevel{Effort: effort}
	}
	// ResolveLevel snaps an unoffered default onto the nearest rung.
	chosen, _ := ai.Model{Reasoning: levels}.ResolveLevel(cmp.Or(l.DefaultEffort, ai.EffortOff))
	for i := range levels {
		levels[i].Default = levels[i].Effort == chosen.Effort
	}
	return levels
}

// namedEfforts reads the effort option's levels, and whether any option says
// reasoning can be switched off. "minimal" has no rung of its own.
func (s modelSpec) namedEfforts() (named []ai.Effort, off bool) {
	for _, option := range s.ReasoningOptions {
		switch option.Type {
		case "toggle":
			off = true
		case "effort":
			for _, value := range option.Values {
				switch value {
				case "none":
					off = true
				case "minimal":
				default:
					named = append(named, ai.Effort(value))
				}
			}
		}
	}
	return named, off
}
