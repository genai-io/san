package input

import (
	"os"

	coreplugin "github.com/genai-io/san/internal/plugin"
)

// pluginTab represents the active tab in the plugin selector.
type pluginTab int

const (
	pluginTabDiscover pluginTab = iota
	pluginTabInstalled
	pluginTabMarketplaces
)

// pluginLevel represents the navigation level within the plugin selector.
type pluginLevel int

const (
	pluginLevelTabList pluginLevel = iota
	pluginLevelDetail
	pluginLevelInstallOptions
	pluginLevelAddMarketplace
	pluginLevelBrowsePlugins
)

type pluginAction struct {
	Label  string
	Action string
}

// pluginItem represents a plugin in the selector.
type pluginItem struct {
	Name        string
	FullName    string
	Description string
	Version     string
	Scope       coreplugin.Scope
	Enabled     bool
	Path        string
	Skills      int
	Commands    int
	Hooks       int
	MCP         int
	LSP         int
	Errors      []string
	Author      string
	Homepage    string
	Marketplace string
}

// pluginDiscoverItem represents a plugin available in a marketplace.
type pluginDiscoverItem struct {
	Name        string
	Description string
	Marketplace string
	Author      string
	Installed   bool
	Homepage    string
	Version     string
}

// pluginMarketplaceItem represents a marketplace in the selector.
type pluginMarketplaceItem struct {
	ID          string
	Source      string
	SourceType  string
	Available   int
	Installed   int
	LastUpdated string
	IsOfficial  bool
}

// PluginSelector holds state for the plugin selector.
type PluginSelector struct {
	registry *coreplugin.Registry

	active         bool
	width          int
	height         int
	lastMessage    string
	isError        bool
	maxVisible     int
	isLoading      bool
	loadingMsg     string
	loadingFrame   int
	loadingTicking bool

	activeTab pluginTab

	installedPlugins  map[coreplugin.Scope][]pluginItem
	installedScopes   []coreplugin.Scope
	installedFlatList []pluginItem
	discoverPlugins   []pluginDiscoverItem
	marketplaces      []pluginMarketplaceItem

	level        pluginLevel
	selectedIdx  int
	scrollOffset int
	detailScroll int

	searchQuery   string
	filteredItems []any

	detailPlugin      *pluginItem
	detailDiscover    *pluginDiscoverItem
	detailMarketplace *pluginMarketplaceItem
	actions           []pluginAction
	actionIdx         int
	parentIdx         int

	addMarketplaceInput string
	addDialogCursor     int

	browseMarketplaceID string
	browsePlugins       []pluginDiscoverItem

	marketplaceManager *coreplugin.MarketplaceManager
	installer          *coreplugin.Installer
}

// NewPluginSelector creates a new PluginSelector.
func NewPluginSelector(reg *coreplugin.Registry) PluginSelector {
	cwd, _ := os.Getwd()
	return PluginSelector{
		registry:           reg,
		active:             false,
		maxVisible:         15,
		activeTab:          pluginTabInstalled,
		installedPlugins:   make(map[coreplugin.Scope][]pluginItem),
		marketplaceManager: coreplugin.NewMarketplaceManager(cwd),
		installer:          coreplugin.NewInstaller(reg, cwd),
	}
}

// IsActive returns whether the selector is active.
func (s *PluginSelector) IsActive() bool {
	return s.active
}
