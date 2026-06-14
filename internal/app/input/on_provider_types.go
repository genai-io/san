// Provider selector: data model, messages, and status-display helpers.
package input

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/llm"
)

// providerTab represents which tab is active in the kit.
type providerTab int

const (
	providerTabModels    providerTab = iota // model selection tab
	providerTabProviders                    // provider management tab
)

// providerItemKind represents a row type in the visible-items list.
type providerItemKind int

const (
	providerItemProviderHeader providerItemKind = iota // non-selectable provider group header (Models tab)
	providerItemModel                                  // selectable model row (Models tab)
	providerItemProvider                               // provider row (Providers tab)
	providerItemAuthMethod                             // expanded auth-method sub-row (Providers tab)
)

// providerListItem is a single row in the flattened visible-items list.
type providerListItem struct {
	Kind        providerItemKind
	Model       *providerModelItem
	Provider    *providerProviderItem
	AuthMethod  *providerAuthMethodItem
	ProviderIdx int // index into allProviders
}

// providerProviderItem represents a provider with its auth methods.
type providerProviderItem struct {
	Provider    llm.Name
	DisplayName string
	AuthMethods []providerAuthMethodItem
	Connected   bool // whether this provider has at least one connected auth method
}

// providerAuthMethodItem represents an auth method in the second level.
type providerAuthMethodItem struct {
	Provider    llm.Name
	AuthMethod  llm.AuthMethod
	DisplayName string
	Status      llm.Status
	EnvVars     []string
}

// providerModelItem represents a model in the kit.
type providerModelItem struct {
	ID               string
	Name             string
	DisplayName      string
	ProviderName     string
	AuthMethod       llm.AuthMethod
	IsCurrent        bool
	InputTokenLimit  int
	OutputTokenLimit int
}

func newProviderModelItem(mdl llm.ModelInfo, providerName string, authMethod llm.AuthMethod, current *llm.CurrentModelInfo) providerModelItem {
	return providerModelItem{
		ID:               mdl.ID,
		Name:             mdl.Name,
		DisplayName:      mdl.DisplayName,
		ProviderName:     providerName,
		AuthMethod:       authMethod,
		IsCurrent:        current != nil && current.ModelID == mdl.ID && string(current.Provider) == providerName,
		InputTokenLimit:  mdl.InputTokenLimit,
		OutputTokenLimit: mdl.OutputTokenLimit,
	}
}

// ProviderSelector holds the state for the unified model & provider kit.
type ProviderSelector struct {
	active bool
	width  int
	height int
	store  *llm.Store

	// Tab
	activeTab providerTab

	// Data
	connectedProviders []providerProviderItem // providers with models (Models tab headers)
	allProviders       []providerProviderItem // all providers (Providers tab)
	allModels          []providerModelItem

	// Flattened visible-items list (rebuilt on state changes)
	visibleItems []providerListItem
	selectedIdx  int
	scrollOffset int
	maxVisible   int

	// Providers tab: expanded provider
	expandedProviderIdx int // index into allProviders; -1 = none

	// Inline API-key input
	apiKeyInput       textinput.Model
	apiKeyActive      bool
	apiKeyEnvVar      string
	apiKeyProviderIdx int // index into allProviders
	apiKeyAuthIdx     int // index into that provider's AuthMethods

	// Inline confirm-remove prompt
	confirmRemoveActive  bool
	confirmRemoveEnvVar  string
	confirmRemoveItemIdx int // index into visibleItems for the pending remove

	// Models tab: search filter and the two flags that disambiguate keys
	// whose meaning depends on what the user is doing.
	searchQuery    string              // active filter text; "" means no filter
	filteredModels []providerModelItem // allModels narrowed to searchQuery

	// searchFocused routes Space: true while the search box has focus (the user
	// is typing a query) so Space inserts a literal space; false while
	// navigating the list so Space marks the highlighted model instead.
	searchFocused bool

	// modelMarked routes Enter: true once the user has explicitly marked a
	// model with Space, so Enter confirms that mark regardless of cursor; false
	// until then, so Enter acts on the highlighted row. (The active model is
	// rendered [*] on open, but that display state is not a mark.)
	modelMarked bool

	// Provider connection result (shown inline)
	lastConnectResult  string
	lastConnectAuthIdx int // item index that triggered the connection
	lastConnectSuccess bool

	// spinnerTick advances on each ProviderConnectingMsg; used to pick a braille
	// frame while a connect/refresh is in flight.
	spinnerTick int
}

// NewProviderSelector creates a new provider selector ProviderSelector.
func NewProviderSelector() ProviderSelector {
	return ProviderSelector{
		active:              false,
		selectedIdx:         0,
		maxVisible:          20,
		expandedProviderIdx: -1,
	}
}

// IsActive returns whether the selector is active.
func (s *ProviderSelector) IsActive() bool {
	return s.active
}

// ProviderModelSelectedMsg is sent when a model is selected.
type ProviderModelSelectedMsg struct {
	ModelID      string
	ProviderName string
	AuthMethod   llm.AuthMethod
}

// ProviderConnectResultMsg is sent when inline connection completes.
type ProviderConnectResultMsg struct {
	AuthIdx   int
	Success   bool
	Message   string
	NewStatus llm.Status
}

// ProviderModelsLoadedMsg is sent when async model loading completes.
type ProviderModelsLoadedMsg struct {
	Models []providerModelItem
}

// providerStatusDisplayInfo contains display information for a provider status.
type providerStatusDisplayInfo struct {
	icon  string
	style lipgloss.Style
	desc  string
}

// providerStatusDisplayMap maps provider status to display information.
var providerStatusDisplayMap = map[llm.Status]providerStatusDisplayInfo{
	llm.StatusConnected: {"●", kit.SelectorStatusConnected(), ""},
	llm.StatusAvailable: {"○", kit.SelectorStatusReady(), "(available)"},
}

// providerGetStatusDisplay returns the icon, style, and description for a provider status.
func providerGetStatusDisplay(status llm.Status) (icon string, style lipgloss.Style, desc string) {
	if info, ok := providerStatusDisplayMap[status]; ok {
		return info.icon, info.style, info.desc
	}
	return "◌", kit.SelectorStatusNone(), ""
}

func providerBestAuthMethodStatus(methods []providerAuthMethodItem) llm.Status {
	for _, m := range methods {
		if m.Status == llm.StatusConnected {
			return llm.StatusConnected
		}
	}
	for _, m := range methods {
		if m.Status == llm.StatusAvailable {
			return llm.StatusAvailable
		}
	}
	return llm.StatusNotConfigured
}
