// Provider selector: unified model & provider selection overlay.
package input

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/secret"
)

// ── State ──────────────────────────────────────────────────────────────────────

// ProviderState holds provider UI state for the TUI model.
// Domain state (LLM, Store, CurrentModel, tokens, thinking) lives
// on the parent app model, not here.
type ProviderState struct {
	FetchingLimits bool
	Selector       ProviderSelector
	StatusMessage  string // Temporary status shown in status bar
	statusToken    int64
}

// SetStatusMessage sets the temporary status message displayed in the status bar.
func (s *ProviderState) SetStatusMessage(msg string) int64 {
	s.statusToken++
	s.StatusMessage = msg
	return s.statusToken
}

// ProviderStatusExpiredMsg is an alias for kit.StatusExpiredMsg.
type ProviderStatusExpiredMsg = kit.StatusExpiredMsg

// ── Runtime ────────────────────────────────────────────────────────────────────

// UpdateProvider routes provider connection and selection messages.
func UpdateProvider(deps OverlayDeps, state *ProviderState, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case ProviderSpinnerTickMsg:
		// Keep ticking while work is in flight; stop once it completes.
		if state.Selector.IsBusy() {
			state.Selector.AdvanceSpinner()
			return providerSpinnerTickCmd(), true
		}
		return nil, true
	case ProviderConnectResultMsg:
		return state.Selector.HandleConnectResult(msg), true
	case ProviderModelSelectedMsg:
		return handleProviderModelSelected(deps, state, msg), true
	case ProviderModelsLoadedMsg:
		state.Selector.HandleModelsLoaded(msg)
		return nil, true
	case ProviderStatusExpiredMsg:
		if msg.Token == state.statusToken {
			state.StatusMessage = ""
		}
		return nil, true
	}
	return nil, false
}

func handleProviderModelSelected(deps OverlayDeps, state *ProviderState, msg ProviderModelSelectedMsg) tea.Cmd {
	_, err := state.Selector.SetModel(msg.ModelID, msg.ProviderName, msg.AuthMethod)
	if err != nil {
		deps.Conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: "Error: " + err.Error()})
		return tea.Batch(deps.CommitMessages()...)
	}

	deps.SetCurrentModel(&llm.CurrentModelInfo{
		ModelID:    msg.ModelID,
		Provider:   llm.Name(msg.ProviderName),
		AuthMethod: msg.AuthMethod,
	})
	ctx := context.Background()
	providerRefreshConnection(deps, state, ctx, llm.Name(msg.ProviderName), msg.AuthMethod)
	return nil
}

func providerRefreshConnection(deps OverlayDeps, state *ProviderState, ctx context.Context, providerName llm.Name, authMethod llm.AuthMethod) {
	p, err := llm.GetProvider(ctx, providerName, authMethod)
	if err != nil {
		log.Logger().Warn("failed to refresh provider connection",
			zap.String("provider", string(providerName)),
			zap.Error(err))
		return
	}
	deps.SwitchProvider(p)
}

// ── Model types ────────────────────────────────────────────────────────────────

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

	// Model search / filter
	searchQuery    string
	filteredModels []providerModelItem

	// Provider connection result (shown inline)
	lastConnectResult  string
	lastConnectAuthIdx int // item index that triggered the connection
	lastConnectSuccess bool

	// spinnerTick advances on each ProviderSpinnerTickMsg; used to pick a braille
	// frame while a connect/refresh is in flight.
	spinnerTick int
}

// providerSpinnerInterval is the spin cadence while a connect/refresh runs —
// fast enough to read as a smooth spinner (independent of the slower global
// thinking-spinner tick).
const providerSpinnerInterval = 90 * time.Millisecond

// ProviderSpinnerTickMsg drives the in-flight braille spinner animation.
type ProviderSpinnerTickMsg struct{}

// providerSpinnerTickCmd schedules the next spinner frame.
func providerSpinnerTickCmd() tea.Cmd {
	return tea.Tick(providerSpinnerInterval, func(time.Time) tea.Msg {
		return ProviderSpinnerTickMsg{}
	})
}

// AdvanceSpinner moves the in-flight spinner to its next frame.
func (s *ProviderSelector) AdvanceSpinner() { s.spinnerTick++ }

// Transient in-flight result markers. While lastConnectResult equals one of
// these, the row shows an animated spinner instead of static text.
const (
	providerStatusRefreshing = "Refreshing..."
	providerStatusConnecting = "Connecting..."
)

// IsBusy reports whether a connect/refresh is in flight, so the spinner-tick
// loop keeps ticking and the row renders an animated frame.
func (s *ProviderSelector) IsBusy() bool {
	return s.active &&
		(s.lastConnectResult == providerStatusRefreshing || s.lastConnectResult == providerStatusConnecting)
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

// NewProviderSelector creates a new provider selector ProviderSelector.
func NewProviderSelector() ProviderSelector {
	return ProviderSelector{
		active:              false,
		selectedIdx:         0,
		maxVisible:          20,
		expandedProviderIdx: -1,
	}
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

// IsActive returns whether the selector is active.
func (s *ProviderSelector) IsActive() bool {
	return s.active
}

// ── Navigation ─────────────────────────────────────────────────────────────────

func (s *ProviderSelector) ensureVisible() {
	if s.selectedIdx < s.scrollOffset {
		s.scrollOffset = s.selectedIdx
	}
	if s.selectedIdx >= s.scrollOffset+s.maxVisible {
		s.scrollOffset = s.selectedIdx - s.maxVisible + 1
	}
}

func (s *ProviderSelector) MoveUp() {
	for s.selectedIdx > 0 {
		s.selectedIdx--
		if s.visibleItems[s.selectedIdx].Kind != providerItemProviderHeader {
			break
		}
	}
	s.ensureVisible()
}

func (s *ProviderSelector) MoveDown() {
	for s.selectedIdx < len(s.visibleItems)-1 {
		s.selectedIdx++
		if s.visibleItems[s.selectedIdx].Kind != providerItemProviderHeader {
			break
		}
	}
	s.ensureVisible()
}

func (s *ProviderSelector) switchTab(t providerTab) {
	if t == s.activeTab {
		return
	}
	s.activeTab = t
	s.resetNavigation()
	s.resetModelSearch()
	s.resetConnectionResult()
	s.expandedProviderIdx = -1
	s.apiKeyActive = false
	s.rebuildVisibleItems()
}

func (s *ProviderSelector) NextTab() { s.switchTab((s.activeTab + 1) % 2) }
func (s *ProviderSelector) PrevTab() { s.switchTab((s.activeTab + 1 + 2) % 2) }

func (s *ProviderSelector) GoBack() bool {
	if s.apiKeyActive {
		s.apiKeyActive = false
		return true
	}
	if s.expandedProviderIdx >= 0 {
		s.expandedProviderIdx = -1
		s.resetConnectionResult()
		s.rebuildVisibleItems()
		return true
	}
	return false
}

func (s *ProviderSelector) clearModelSearch() bool {
	if s.searchQuery == "" {
		return false
	}
	s.searchQuery = ""
	s.rebuildVisibleItems()
	return true
}

func (s *ProviderSelector) trimModelSearch() {
	if len(s.searchQuery) == 0 {
		return
	}
	s.searchQuery = s.searchQuery[:len(s.searchQuery)-1]
	s.rebuildVisibleItems()
}

func (s *ProviderSelector) appendModelSearch(text string) {
	s.searchQuery += text
	s.rebuildVisibleItems()
}

func (s *ProviderSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	// Route to API key input if active
	if s.apiKeyActive {
		return s.handleAPIKeyInput(key)
	}

	switch key.Type {
	case tea.KeyTab:
		if s.searchQuery == "" {
			s.NextTab()
		}
		return nil

	case tea.KeyShiftTab:
		if s.searchQuery == "" {
			s.PrevTab()
		}
		return nil

	case tea.KeyUp, tea.KeyCtrlP:
		s.MoveUp()
		return nil

	case tea.KeyDown, tea.KeyCtrlN:
		s.MoveDown()
		return nil

	case tea.KeyEnter:
		return s.Select()

	case tea.KeyRight:
		if s.searchQuery == "" {
			s.NextTab()
		}
		return nil

	case tea.KeyLeft:
		if s.searchQuery == "" && !s.GoBack() {
			s.PrevTab()
		}
		return nil

	case tea.KeyEsc:
		if s.clearModelSearch() {
			return nil
		}
		if s.GoBack() {
			return nil
		}
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }

	case tea.KeyBackspace:
		s.trimModelSearch()
		return nil

	case tea.KeySpace:
		s.appendModelSearch(" ")
		return nil

	case tea.KeyRunes:
		s.appendModelSearch(string(key.Runes))
		return nil
	}

	// Vim navigation (only when search query is empty)
	if s.searchQuery == "" {
		switch key.String() {
		case "j":
			s.MoveDown()
		case "k":
			s.MoveUp()
		case "l":
			s.NextTab()
		case "h":
			if !s.GoBack() {
				s.PrevTab()
			}
		}
	}

	return nil
}

func (s *ProviderSelector) handleAPIKeyInput(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyEnter:
		value := strings.TrimSpace(s.apiKeyInput.Value())
		if value == "" {
			return nil
		}
		if store := secret.Default(); store != nil {
			_ = store.Set(s.apiKeyEnvVar, value)
		}
		os.Setenv(s.apiKeyEnvVar, value)
		s.apiKeyActive = false

		// Find the auth method and trigger connection
		if s.apiKeyProviderIdx >= 0 && s.apiKeyProviderIdx < len(s.allProviders) {
			dp := &s.allProviders[s.apiKeyProviderIdx]
			if s.apiKeyAuthIdx >= 0 && s.apiKeyAuthIdx < len(dp.AuthMethods) {
				am := dp.AuthMethods[s.apiKeyAuthIdx]
				return s.connectAuthMethod(am, s.selectedIdx)
			}
		}
		return nil

	case tea.KeyEsc:
		s.apiKeyActive = false
		return nil

	default:
		var cmd tea.Cmd
		s.apiKeyInput, cmd = s.apiKeyInput.Update(key)
		return cmd
	}
}

// ── Selection ──────────────────────────────────────────────────────────────────

func (s *ProviderSelector) Select() tea.Cmd {
	if s.selectedIdx < 0 || s.selectedIdx >= len(s.visibleItems) {
		return nil
	}

	item := s.visibleItems[s.selectedIdx]
	switch item.Kind {
	case providerItemModel:
		return s.selectModel(item.Model)
	case providerItemProvider:
		return s.selectProvider(item)
	case providerItemAuthMethod:
		return s.selectAuthMethod(item)
	default:
		return nil
	}
}

func (s *ProviderSelector) selectModel(m *providerModelItem) tea.Cmd {
	if m == nil {
		return nil
	}
	s.active = false
	return func() tea.Msg {
		return ProviderModelSelectedMsg{
			ModelID:      m.ID,
			ProviderName: m.ProviderName,
			AuthMethod:   m.AuthMethod,
		}
	}
}

// selectProvider handles Enter on a provider row (Providers tab).
// Connected single auth method: refresh models.
// Disconnected single auth method: auto-connect or show API key input.
// Multiple auth methods: expand inline to show auth method list.
func (s *ProviderSelector) selectProvider(item providerListItem) tea.Cmd {
	if item.Provider == nil {
		return nil
	}
	p := item.Provider

	if len(p.AuthMethods) == 1 {
		am := p.AuthMethods[0]
		if am.Status == llm.StatusConnected {
			// Refresh: re-fetch models for this connected provider
			return s.refreshAuthMethod(am, s.selectedIdx)
		}
		return s.tryConnectOrPromptKey(am, item.ProviderIdx, 0)
	}

	if len(p.AuthMethods) == 0 {
		return nil
	}

	// Multiple auth methods: toggle inline expansion
	if s.expandedProviderIdx == item.ProviderIdx {
		s.expandedProviderIdx = -1
	} else {
		s.expandedProviderIdx = item.ProviderIdx
	}
	s.resetConnectionResult()
	s.rebuildVisibleItems()
	return nil
}

func (s *ProviderSelector) selectAuthMethod(item providerListItem) tea.Cmd {
	if item.AuthMethod == nil {
		return nil
	}
	am := item.AuthMethod

	if am.Status == llm.StatusConnected {
		// Refresh: re-fetch models for this connected auth method
		return s.refreshAuthMethod(*am, s.selectedIdx)
	}

	return s.tryConnectOrPromptKey(*am, item.ProviderIdx, s.findAuthMethodIndex(item))
}

// tryConnectOrPromptKey connects if env vars are available, otherwise shows API key input.
func (s *ProviderSelector) tryConnectOrPromptKey(am providerAuthMethodItem, providerIdx, authIdx int) tea.Cmd {
	if am.Status == llm.StatusAvailable || providerIsEnvReady(am.EnvVars) {
		return s.connectAuthMethod(am, s.selectedIdx)
	}

	// Show inline API key input
	envVar := providerFirstEnvVar(am.EnvVars)
	if envVar == "" {
		return nil
	}
	s.apiKeyProviderIdx = providerIdx
	s.apiKeyAuthIdx = authIdx
	s.initAPIKeyInput(envVar)
	return nil
}

func (s *ProviderSelector) findAuthMethodIndex(item providerListItem) int {
	if item.AuthMethod == nil || item.ProviderIdx < 0 || item.ProviderIdx >= len(s.allProviders) {
		return 0
	}
	p := &s.allProviders[item.ProviderIdx]
	for i, am := range p.AuthMethods {
		if am.Provider == item.AuthMethod.Provider && am.AuthMethod == item.AuthMethod.AuthMethod {
			return i
		}
	}
	return 0
}

func providerIsEnvReady(envVars []string) bool {
	for _, v := range envVars {
		if v != "" && secret.Resolve(v) != "" {
			return true
		}
	}
	return false
}

func providerFirstEnvVar(envVars []string) string {
	for _, v := range envVars {
		if v != "" {
			return v
		}
	}
	return ""
}

// ── Loading ────────────────────────────────────────────────────────────────────

// providerOrder defines the display order for providers.
var providerOrder = []llm.Name{
	llm.Anthropic,
	llm.OpenAI,
	llm.Google,
	llm.DeepSeek,
	llm.MinMax,
	llm.Moonshot,
	llm.Alibaba,
	llm.BigModel,
}

// providerDisplayNames maps provider to human-readable name.
var providerDisplayNames = map[llm.Name]string{
	llm.Anthropic: "Anthropic",
	llm.OpenAI:    "OpenAI",
	llm.Google:    "Google",
	llm.DeepSeek:  "DeepSeek",
	llm.MinMax:    "MiniMax",
	llm.Moonshot:  "Moonshot",
	llm.Alibaba:   "Alibaba",
	llm.BigModel:  "Z.ai (GLM series)",
}

// Enter opens the unified model & provider kit.
func (s *ProviderSelector) Enter(ctx context.Context, width, height int) (tea.Cmd, error) {
	s.resetNavigation()
	s.resetModelSearch()
	s.resetConnectionResult()
	s.expandedProviderIdx = -1
	s.apiKeyActive = false
	s.active = true
	s.activeTab = providerTabModels
	s.width = width
	s.height = height

	cmd, err := s.loadProviderData()
	if err != nil {
		return nil, err
	}
	s.rebuildVisibleItems()
	return cmd, nil
}

// loadProviderData refreshes provider and model data from a fresh store.
// Does NOT reset UI state (tabs, selection, expansion) or call rebuildVisibleItems.
func (s *ProviderSelector) loadProviderData() (tea.Cmd, error) {
	store, err := llm.NewStore()
	if err != nil {
		return nil, fmt.Errorf("failed to load store: %w", err)
	}
	s.store = store

	providersWithStatus := llm.GetProvidersWithStatus(store)

	s.connectedProviders = nil
	s.allProviders = nil

	for _, p := range providerOrder {
		infos, ok := providersWithStatus[p]
		if !ok || len(infos) == 0 {
			continue
		}

		item := providerProviderItem{
			Provider:    p,
			DisplayName: providerDisplayNames[p],
			AuthMethods: make([]providerAuthMethodItem, 0, len(infos)),
		}

		connected := false
		for _, info := range infos {
			item.AuthMethods = append(item.AuthMethods, providerAuthMethodItem{
				Provider:    info.Meta.Provider,
				AuthMethod:  info.Meta.AuthMethod,
				DisplayName: info.Meta.DisplayName,
				Status:      info.Status,
				EnvVars:     info.Meta.EnvVars,
			})
			if info.Status == llm.StatusConnected {
				connected = true
			}
		}

		item.Connected = connected
		s.allProviders = append(s.allProviders, item)
		if connected {
			s.connectedProviders = append(s.connectedProviders, item)
		}
	}

	current := store.GetCurrentModel()

	s.allModels = nil
	allCached := store.GetAllCachedModels()
	if len(allCached) == 0 {
		allCached = store.GetAllCachedModelsIncludeExpired()
	}

	var asyncCmd tea.Cmd
	if len(allCached) > 0 {
		s.loadModelsCached(allCached, current)
	} else {
		asyncCmd = s.loadModelsAsync(store, current)
	}

	s.ensureModelProvidersExist()
	s.sortConnectedProviders(current)

	return asyncCmd, nil
}

// ensureModelProvidersExist ensures every provider that has cached models
// is represented in connectedProviders (handles cases where registry doesn't
// have the provider registered but models exist in cache).
func (s *ProviderSelector) ensureModelProvidersExist() {
	existing := make(map[string]bool)
	for _, cp := range s.connectedProviders {
		existing[string(cp.Provider)] = true
	}

	// Collect unique provider names from models
	seen := make(map[string]bool)
	for _, m := range s.allModels {
		if existing[m.ProviderName] || seen[m.ProviderName] {
			continue
		}
		seen[m.ProviderName] = true

		displayName := providerDisplayNames[llm.Name(m.ProviderName)]
		if displayName == "" {
			displayName = m.ProviderName
		}

		s.connectedProviders = append(s.connectedProviders, providerProviderItem{
			Provider:    llm.Name(m.ProviderName),
			DisplayName: displayName,
			Connected:   true,
		})
	}
}

// loadModelsAsync returns a tea.Cmd that fetches models from all connected
// providers concurrently, sending a ProviderModelsLoadedMsg when done.
func (s *ProviderSelector) loadModelsAsync(store *llm.Store, current *llm.CurrentModelInfo) tea.Cmd {
	connections := store.GetConnections()
	return func() tea.Msg {
		ctx := context.Background()

		type providerResult struct {
			providerName string
			authMethod   llm.AuthMethod
			models       []llm.ModelInfo
		}

		ch := make(chan providerResult, len(connections))
		var wg sync.WaitGroup

		for name, conn := range connections {
			wg.Add(1)
			go func(providerName string, authMethod llm.AuthMethod) {
				defer wg.Done()
				p, err := llm.GetProvider(ctx, llm.Name(providerName), authMethod)
				if err != nil {
					return
				}
				mdls, err := p.ListModels(ctx)
				if err != nil {
					return
				}
				ch <- providerResult{providerName, authMethod, mdls}
			}(name, conn.AuthMethod)
		}

		go func() { wg.Wait(); close(ch) }()

		var models []providerModelItem
		for r := range ch {
			prov := llm.Name(r.providerName)
			_ = store.CacheModels(prov, r.authMethod, r.models)

			for _, mdl := range r.models {
				models = append(models, newProviderModelItem(mdl, r.providerName, r.authMethod, current))
			}
		}
		return ProviderModelsLoadedMsg{Models: models}
	}
}

// HandleModelsLoaded updates the panel with asynchronously loaded models.
func (s *ProviderSelector) HandleModelsLoaded(msg ProviderModelsLoadedMsg) {
	s.allModels = msg.Models
	s.ensureModelProvidersExist()

	var current *llm.CurrentModelInfo
	if s.store != nil {
		current = s.store.GetCurrentModel()
	}
	s.sortConnectedProviders(current)
	s.rebuildVisibleItems()
}

// loadModelsCached loads models from the store cache.
func (s *ProviderSelector) loadModelsCached(allCached map[string][]llm.ModelInfo, current *llm.CurrentModelInfo) {
	for key, models := range allCached {
		parts := strings.SplitN(key, ":", 2)
		providerName := key
		var authMethod llm.AuthMethod
		if len(parts) >= 2 {
			providerName = parts[0]
			authMethod = llm.AuthMethod(parts[1])
		}

		for _, mdl := range models {
			s.allModels = append(s.allModels, newProviderModelItem(mdl, providerName, authMethod, current))
		}
	}
}

// sortConnectedProviders sorts connected providers so that the current
// selection's provider comes first, then alphabetical.
func (s *ProviderSelector) sortConnectedProviders(current *llm.CurrentModelInfo) {
	if current == nil {
		return
	}
	currentProvider := current.Provider
	sort.SliceStable(s.connectedProviders, func(i, j int) bool {
		iMatch := s.connectedProviders[i].Provider == currentProvider
		jMatch := s.connectedProviders[j].Provider == currentProvider
		if iMatch != jMatch {
			return iMatch
		}
		return false
	})
}

// rebuildVisibleItems constructs the flat visible-items list from current state.
func (s *ProviderSelector) rebuildVisibleItems() {
	s.visibleItems = nil

	switch s.activeTab {
	case providerTabModels:
		s.rebuildModelsTab()
	case providerTabProviders:
		s.rebuildProvidersTab()
	}

	s.clampSelection()
}

// rebuildModelsTab builds visible items for the Models tab.
func (s *ProviderSelector) rebuildModelsTab() {
	s.applyFilter()

	// Group filtered models by provider
	providerModels := make(map[string][]providerModelItem)
	for i := range s.filteredModels {
		m := &s.filteredModels[i]
		providerModels[m.ProviderName] = append(providerModels[m.ProviderName], *m)
	}

	for i := range s.connectedProviders {
		cp := &s.connectedProviders[i]
		models := providerModels[string(cp.Provider)]
		if len(models) == 0 && s.searchQuery != "" {
			continue
		}

		s.visibleItems = append(s.visibleItems, providerListItem{
			Kind:        providerItemProviderHeader,
			Provider:    cp,
			ProviderIdx: i,
		})

		// Sort models: current first
		sort.SliceStable(models, func(a, b int) bool {
			return models[a].IsCurrent && !models[b].IsCurrent
		})

		for j := range models {
			s.visibleItems = append(s.visibleItems, providerListItem{
				Kind:        providerItemModel,
				Model:       &models[j],
				ProviderIdx: i,
			})
		}
	}
}

// rebuildProvidersTab builds visible items for the Providers tab.
func (s *ProviderSelector) rebuildProvidersTab() {
	for i := range s.allProviders {
		p := &s.allProviders[i]

		// Apply search filter on provider name
		if s.searchQuery != "" {
			query := strings.ToLower(s.searchQuery)
			if !kit.FuzzyMatch(strings.ToLower(p.DisplayName), query) &&
				!kit.FuzzyMatch(strings.ToLower(string(p.Provider)), query) {
				continue
			}
		}

		s.visibleItems = append(s.visibleItems, providerListItem{
			Kind:        providerItemProvider,
			Provider:    p,
			ProviderIdx: i,
		})

		// Show expanded auth methods
		if s.expandedProviderIdx == i {
			for j := range p.AuthMethods {
				s.visibleItems = append(s.visibleItems, providerListItem{
					Kind:        providerItemAuthMethod,
					AuthMethod:  &p.AuthMethods[j],
					ProviderIdx: i,
				})
			}
		}
	}
}

func (s *ProviderSelector) applyFilter() {
	if s.searchQuery == "" {
		s.filteredModels = s.allModels
		return
	}
	query := strings.ToLower(s.searchQuery)
	s.filteredModels = nil
	for _, m := range s.allModels {
		if kit.FuzzyMatch(strings.ToLower(m.ID), query) ||
			kit.FuzzyMatch(strings.ToLower(m.DisplayName), query) ||
			kit.FuzzyMatch(strings.ToLower(m.ProviderName), query) {
			s.filteredModels = append(s.filteredModels, m)
		}
	}
}

func (s *ProviderSelector) clampSelection() {
	if len(s.visibleItems) == 0 {
		s.selectedIdx = 0
		return
	}
	if s.selectedIdx >= len(s.visibleItems) {
		s.selectedIdx = len(s.visibleItems) - 1
	}
	if s.selectedIdx < 0 {
		s.selectedIdx = 0
	}
	// Skip non-selectable items forward
	if s.visibleItems[s.selectedIdx].Kind == providerItemProviderHeader {
		for s.selectedIdx < len(s.visibleItems)-1 {
			s.selectedIdx++
			if s.visibleItems[s.selectedIdx].Kind != providerItemProviderHeader {
				break
			}
		}
	}
}

// refreshAuthMethod re-fetches models for an already connected provider auth method.
func (s *ProviderSelector) refreshAuthMethod(item providerAuthMethodItem, authIdx int) tea.Cmd {
	if s.IsBusy() {
		// A connect/refresh is already in flight; ignore re-entry so we don't
		// start a second spinner-tick loop or a concurrent store write.
		return nil
	}
	s.lastConnectResult = providerStatusRefreshing
	s.lastConnectAuthIdx = authIdx
	s.lastConnectSuccess = false

	work := func() tea.Msg {
		ctx := context.Background()

		llmProvider, err := llm.GetProvider(ctx, item.Provider, item.AuthMethod)
		if err != nil {
			return ProviderConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: fmt.Sprintf("failed to load models for %s: %s", item.Provider, err.Error()),
			}
		}

		models, err := llmProvider.ListModels(ctx)

		store, _ := llm.NewStore()
		if store != nil && len(models) > 0 {
			_ = store.CacheModels(item.Provider, item.AuthMethod, models)
		}

		if err != nil && len(models) == 0 {
			return ProviderConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: fmt.Sprintf("failed to load models for %s: %s", item.Provider, err.Error()),
			}
		}

		if err != nil {
			return ProviderConnectResultMsg{
				AuthIdx:   authIdx,
				Success:   true,
				Message:   fmt.Sprintf("⚠ %d models loaded with refresh warning", len(models)),
				NewStatus: llm.StatusConnected,
			}
		}

		return ProviderConnectResultMsg{
			AuthIdx:   authIdx,
			Success:   true,
			Message:   fmt.Sprintf("● %d models", len(models)),
			NewStatus: llm.StatusConnected,
		}
	}
	// Start the spinner alongside the async work.
	return tea.Batch(providerSpinnerTickCmd(), work)
}

// connectAuthMethod initiates an async connection to a provider auth method.
func (s *ProviderSelector) connectAuthMethod(item providerAuthMethodItem, authIdx int) tea.Cmd {
	if s.IsBusy() {
		// A connect/refresh is already in flight; ignore re-entry so we don't
		// start a second spinner-tick loop or a concurrent store write.
		return nil
	}
	s.lastConnectResult = providerStatusConnecting
	s.lastConnectAuthIdx = authIdx
	s.lastConnectSuccess = false

	work := func() tea.Msg {
		ctx := context.Background()
		result, err := s.ConnectProvider(ctx, item.Provider, item.AuthMethod)
		if err != nil {
			return ProviderConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: err.Error(),
			}
		}

		return ProviderConnectResultMsg{
			AuthIdx:   authIdx,
			Success:   true,
			Message:   result,
			NewStatus: llm.StatusConnected,
		}
	}
	return tea.Batch(providerSpinnerTickCmd(), work)
}

// HandleConnectResult updates the selector state with connection result.
func (s *ProviderSelector) HandleConnectResult(msg ProviderConnectResultMsg) tea.Cmd {
	s.lastConnectAuthIdx = msg.AuthIdx
	s.lastConnectResult = msg.Message
	s.lastConnectSuccess = msg.Success

	if !msg.Success {
		return nil
	}

	// Reload provider/model data, preserving UI state (tab, expansion, result).
	cmd, _ := s.loadProviderData()
	s.rebuildVisibleItems()
	return cmd
}

// ConnectProvider connects to a provider and verifies the connection.
func (s *ProviderSelector) ConnectProvider(ctx context.Context, p llm.Name, authMethod llm.AuthMethod) (string, error) {
	if s.store == nil {
		store, err := llm.NewStore()
		if err != nil {
			return "", fmt.Errorf("failed to load store: %w", err)
		}
		s.store = store
	}

	meta, ok := llm.GetMeta(p, authMethod)
	if !ok {
		return "", fmt.Errorf("provider not found: %s:%s", p, authMethod)
	}

	if !llm.IsReady(meta) {
		missingVars := []string{}
		for _, envVar := range meta.EnvVars {
			if envVar == "" {
				continue
			}
			missingVars = append(missingVars, envVar)
		}
		return "", fmt.Errorf("missing required environment variables: %s", strings.Join(missingVars, ", "))
	}

	llmProvider, err := llm.GetProvider(ctx, p, authMethod)
	if err != nil {
		return "", fmt.Errorf("failed to create provider: %w", err)
	}

	models, listErr := llmProvider.ListModels(ctx)
	if listErr != nil && len(models) == 0 {
		return "", fmt.Errorf("failed to load models for %s: %w", meta.DisplayName, listErr)
	}
	if len(models) > 0 {
		_ = s.store.CacheModels(p, authMethod, models)
	}

	if err := s.store.Connect(p, authMethod); err != nil {
		return "", fmt.Errorf("failed to save connection: %w", err)
	}

	if listErr != nil {
		return fmt.Sprintf("Connected to %s via %s (%d models; refresh warning: %v)", meta.DisplayName, authMethod, len(models), listErr), nil
	}

	return fmt.Sprintf("Connected to %s via %s (%d models)", meta.DisplayName, authMethod, len(models)), nil
}

// SetModel sets the current model.
func (s *ProviderSelector) SetModel(modelID string, providerName string, authMethod llm.AuthMethod) (string, error) {
	if s.store == nil {
		store, err := llm.NewStore()
		if err != nil {
			return "", fmt.Errorf("failed to load store: %w", err)
		}
		s.store = store
	}

	if err := s.store.SetCurrentModel(modelID, llm.Name(providerName), authMethod); err != nil {
		return "", fmt.Errorf("failed to set model: %w", err)
	}

	return fmt.Sprintf("Model set to: %s (%s)", modelID, providerName), nil
}

// initAPIKeyInput initializes the textinput for API key entry.
func (s *ProviderSelector) initAPIKeyInput(envVar string) {
	ti := textinput.New()
	ti.Placeholder = envVar
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 40
	ti.EchoMode = textinput.EchoPassword
	s.apiKeyInput = ti
	s.apiKeyActive = true
	s.apiKeyEnvVar = envVar
}

// ── Reset ──────────────────────────────────────────────────────────────────────

func (s *ProviderSelector) resetConnectionResult() {
	s.lastConnectResult = ""
	s.lastConnectAuthIdx = 0
	s.lastConnectSuccess = false
}

func (s *ProviderSelector) resetModelSearch() {
	s.searchQuery = ""
	s.filteredModels = nil
	s.scrollOffset = 0
}

func (s *ProviderSelector) resetNavigation() {
	s.selectedIdx = 0
	s.scrollOffset = 0
}

// Cancel cancels the selector and clears transient state so the next open starts cleanly.
func (s *ProviderSelector) Cancel() {
	s.active = false
	s.connectedProviders = nil
	s.allProviders = nil
	s.allModels = nil
	s.filteredModels = nil
	s.visibleItems = nil
	s.expandedProviderIdx = -1
	s.apiKeyActive = false
	s.store = nil
	s.resetNavigation()
	s.resetModelSearch()
	s.resetConnectionResult()
}

// ── Helpers ────────────────────────────────────────────────────────────────────

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
