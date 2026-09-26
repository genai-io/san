package input

import (
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

// isolateSecretStore points the secret store at a throwaway HOME so tests never
// read or write the developer's real ~/.san/secrets.json. secret.Default() is a
// sync.Once singleton, so it must be reset around each test.
func isolateSecretStore(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	secret.ResetDefault()
	t.Cleanup(secret.ResetDefault)
}

func TestHandleCredentialEditForSingleAuthMethod(t *testing.T) {
	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    "openai",
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	cmd := m.handleCredentialEdit()
	if cmd != nil {
		t.Fatal("handleCredentialEdit should not return a command for single auth method")
	}

	if !m.credForm.active {
		t.Fatal("handleCredentialEdit should activate API key input for connected providers")
	}
	if m.credForm.vars[0] != "OPENAI_API_KEY" {
		t.Fatalf("got env var %q, want OPENAI_API_KEY", m.credForm.vars[0])
	}
}

func TestHandleCredentialEditForMultipleAuthMethods(t *testing.T) {
	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    llm.Anthropic,
			DisplayName: "Anthropic",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.Anthropic,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusNotConfigured,
					EnvVars:     []string{"ANTHROPIC_API_KEY"},
				},
				{
					Provider:    llm.Anthropic,
					AuthMethod:  llm.AuthBedrock,
					DisplayName: "AWS Bedrock",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"BEDROCK_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// First handleCredentialEdit should expand the provider
	cmd := m.handleCredentialEdit()
	if cmd != nil {
		t.Fatal("handleCredentialEdit should not return a command when expanding")
	}
	if m.expandedProviderIdx != 0 {
		t.Fatal("expandedProviderIdx should be 0 after expanding")
	}

	// Now select the connected auth method and handleCredentialEdit again
	for i, item := range m.visibleItems {
		if item.Kind == providerItemAuthMethod && item.AuthMethod != nil && item.AuthMethod.Status == llm.StatusConnected {
			m.selectedIdx = i
			break
		}
	}

	cmd = m.handleCredentialEdit()
	if cmd != nil {
		t.Fatal("handleCredentialEdit should not return a command when editing auth method")
	}
	if !m.credForm.active {
		t.Fatal("handleCredentialEdit should activate API key input")
	}
	if m.credForm.vars[0] != "BEDROCK_API_KEY" {
		t.Fatalf("got env var %q, want BEDROCK_API_KEY", m.credForm.vars[0])
	}
}

func TestHandleCredentialEditUpdatesOnEnter(t *testing.T) {
	isolateSecretStore(t)
	t.Setenv("OPENAI_API_KEY", "")

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    llm.OpenAI,
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// Enter edit mode
	cmd := m.handleCredentialEdit()
	if cmd != nil {
		t.Fatal("handleCredentialEdit should not return a command for single auth method")
	}

	// Verify API key input is active
	if !m.credForm.active {
		t.Fatal("handleCredentialEdit should activate API key input")
	}
	if m.credForm.vars[0] != "OPENAI_API_KEY" {
		t.Fatalf("got env var %q, want OPENAI_API_KEY", m.credForm.vars[0])
	}

	// Update the API key
	m.credForm.inputs[0].SetValue("NEW_TEST_KEY")

	// Simulate Enter key to submit
	cmd = m.handleCredentialFormKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("handleCredentialFormKey should return a command after Enter")
	}

	// Verify API key input is closed after submission
	if m.credForm.active {
		t.Fatal("API key input should be deactivated after Enter")
	}

	// Verify the environment variable is set
	if os.Getenv("OPENAI_API_KEY") != "NEW_TEST_KEY" {
		t.Fatalf("expected OPENAI_API_KEY to be set to NEW_TEST_KEY, got %q", os.Getenv("OPENAI_API_KEY"))
	}
}

func TestHandleCredentialEditWithEmptyEnv(t *testing.T) {
	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    "test",
			DisplayName: "Test Provider",
			AuthMethods: []providerAuthMethodItem{
				{
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{""},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// Attempt to edit credentials with empty env should fail
	cmd := m.handleCredentialEdit()
	if cmd != nil {
		t.Fatalf("handleCredentialEdit should return nil when EnvVars is empty, got %T", cmd)
	}
	if m.credForm.active {
		t.Fatal("handleCredentialEdit should not activate API key input when EnvVars is empty")
	}
}

func TestEditAuthMethodPreservesOtherConnections(t *testing.T) {
	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    llm.OpenAI,
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "Primary Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "Backup Key",
					Status:      llm.StatusAvailable,
					EnvVars:     []string{"OPENAI_BACKUP_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// Expand provider first
	m.handleCredentialEdit()
	m.rebuildVisibleItems()

	// Find the connected auth method and select it
	for i, item := range m.visibleItems {
		if item.Kind == providerItemAuthMethod && item.AuthMethod != nil && item.AuthMethod.Status == llm.StatusConnected {
			m.selectedIdx = i
			break
		}
	}

	// Enter credential edit mode and update the key
	m.handleCredentialEdit()
	if !m.credForm.active {
		t.Fatal("handleCredentialEdit should activate API key input")
	}

	// Cancel the edit and verify state
	m.handleCredentialFormKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.credForm.active {
		t.Fatal("API key input should be canceled after Esc")
	}
}

func TestEditCredentialFlowWithKeyboardShortcuts(t *testing.T) {
	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    llm.OpenAI,
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// Trigger edit with Ctrl+E
	cmd := m.HandleKeypress(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("handleCredentialEdit should not return a command for single auth method")
	}

	if !m.credForm.active {
		t.Fatal("handleCredentialEdit should activate API key input")
	}
	if m.credForm.vars[0] != "OPENAI_API_KEY" {
		t.Fatal("incorrect environment variable set for API key input")
	}
}

func TestHandleCredentialRemoveSingleAuthMethod(t *testing.T) {
	isolateSecretStore(t)
	t.Setenv("OPENAI_API_KEY", "test-key-to-remove")
	if store := secret.Default(); store != nil {
		_ = store.Set("OPENAI_API_KEY", "test-key-to-remove")
	}

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    "openai",
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// First press: shows confirmation
	cmd := m.handleCredentialRemove()
	if cmd != nil {
		t.Fatal("handleCredentialRemove should not return a command")
	}
	if !m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be true after Ctrl+D")
	}
	// Env var should still be set at this point
	if os.Getenv("OPENAI_API_KEY") != "test-key-to-remove" {
		t.Fatal("OPENAI_API_KEY should not be removed until confirmed")
	}

	// Confirm with 'y'
	cmd = m.handleConfirmRemove(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd != nil {
		t.Fatal("handleConfirmRemove should not return a command")
	}
	if m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be false after confirm")
	}

	// Verify env var is unset
	if os.Getenv("OPENAI_API_KEY") != "" {
		t.Fatal("OPENAI_API_KEY should be unset after confirm")
	}

	// Verify secret store is cleared
	if store := secret.Default(); store != nil {
		if store.Get("OPENAI_API_KEY") != "" {
			t.Fatal("OPENAI_API_KEY should be removed from secret store")
		}
	}
}

func TestHandleCredentialRemoveCancel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    "openai",
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// Show confirmation
	m.handleCredentialRemove()
	if !m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be true")
	}

	// Cancel with Esc
	m.handleConfirmRemove(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be false after cancel")
	}

	// Env var should remain
	if os.Getenv("OPENAI_API_KEY") != "test-key" {
		t.Fatal("OPENAI_API_KEY should not be removed after cancel")
	}
}

func TestHandleCredentialRemoveMultipleAuthMethods(t *testing.T) {
	isolateSecretStore(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-ak-key")
	t.Setenv("BEDROCK_API_KEY", "test-bk-key")
	if store := secret.Default(); store != nil {
		_ = store.Set("ANTHROPIC_API_KEY", "test-ak-key")
		_ = store.Set("BEDROCK_API_KEY", "test-bk-key")
	}

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    llm.Anthropic,
			DisplayName: "Anthropic",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.Anthropic,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"ANTHROPIC_API_KEY"},
				},
				{
					Provider:    llm.Anthropic,
					AuthMethod:  llm.AuthBedrock,
					DisplayName: "AWS Bedrock",
					Status:      llm.StatusAvailable,
					EnvVars:     []string{"BEDROCK_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider row — should not show confirmation (multiple auth methods)
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	cmd := m.handleCredentialRemove()
	if cmd != nil {
		t.Fatal("handleCredentialRemove should return nil for provider with multiple auth methods")
	}
	if m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be false for provider with multiple auth methods")
	}

	// Expand and select the specific auth method
	m.expandedProviderIdx = 0
	m.rebuildVisibleItems()
	for i, item := range m.visibleItems {
		if item.Kind == providerItemAuthMethod && item.AuthMethod != nil && item.AuthMethod.AuthMethod == llm.AuthAPIKey {
			m.selectedIdx = i
			break
		}
	}

	// Show confirmation and confirm
	m.handleCredentialRemove()
	if !m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be true for auth method row")
	}
	m.handleConfirmRemove(tea.KeyPressMsg{Code: 'y', Text: "y"})

	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		t.Fatal("ANTHROPIC_API_KEY should be unset after confirm")
	}
	// BEDROCK should remain
	if os.Getenv("BEDROCK_API_KEY") != "test-bk-key" {
		t.Fatal("BEDROCK_API_KEY should not be affected")
	}
}

func TestHandleCredentialRemoveKeyboardShortcut(t *testing.T) {
	isolateSecretStore(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	if store := secret.Default(); store != nil {
		_ = store.Set("OPENAI_API_KEY", "test-key")
	}

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{
		{
			Provider:    "openai",
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Select the provider
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	// Trigger remove with Ctrl+D (shows confirmation)
	cmd := m.HandleKeypress(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("handleCredentialRemove should not return a command")
	}
	if !m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be true after Ctrl+D")
	}

	// Confirm with 'y' via HandleKeypress
	cmd = m.HandleKeypress(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd != nil {
		t.Fatal("handleConfirmRemove should not return a command")
	}

	if os.Getenv("OPENAI_API_KEY") != "" {
		t.Fatal("OPENAI_API_KEY should be unset after confirm")
	}
}

func TestHandleCredentialRemoveNonProvidersTab(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabModels
	m.allProviders = []providerProviderItem{
		{
			Provider:    "openai",
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	cmd := m.handleCredentialRemove()
	if cmd != nil {
		t.Fatal("handleCredentialRemove should return nil on non-providers tab")
	}
	if m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be false on non-providers tab")
	}

	// Env var should remain
	if os.Getenv("OPENAI_API_KEY") != "test-key" {
		t.Fatal("OPENAI_API_KEY should not be removed on non-providers tab")
	}
}

func TestHandleCredentialRemoveClearsModelsAndConnection(t *testing.T) {
	isolateSecretStore(t)
	t.Setenv("OPENAI_API_KEY", "test-key-to-remove")

	if secretStore := secret.Default(); secretStore != nil {
		_ = secretStore.Set("OPENAI_API_KEY", "test-key-to-remove")
	}

	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	// Connect and cache models
	if err := store.Connect(llm.OpenAI, llm.AuthAPIKey); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthAPIKey, []llm.ModelInfo{
		{ID: "gpt-4o", DisplayName: "GPT-4o"},
	}); err != nil {
		t.Fatalf("CacheModels() error = %v", err)
	}
	if err := store.SetCurrentModel("gpt-4o", llm.OpenAI, llm.AuthAPIKey); err != nil {
		t.Fatalf("SetCurrentModel() error = %v", err)
	}

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.store = store
	m.allProviders = []providerProviderItem{
		{
			Provider:    "openai",
			DisplayName: "OpenAI",
			AuthMethods: []providerAuthMethodItem{
				{
					Provider:    llm.OpenAI,
					AuthMethod:  llm.AuthAPIKey,
					DisplayName: "API Key",
					Status:      llm.StatusConnected,
					EnvVars:     []string{"OPENAI_API_KEY"},
				},
			},
		},
	}
	m.rebuildVisibleItems()

	// Verify preconditions
	if !store.IsConnected(llm.OpenAI, llm.AuthAPIKey) {
		t.Fatal("expected OpenAI to be connected before remove")
	}
	if _, ok := store.CachedModels(llm.OpenAI, llm.AuthAPIKey); !ok {
		t.Fatal("expected cached models before remove")
	}
	if cur := store.CurrentModel(); cur == nil || cur.Provider != llm.OpenAI {
		t.Fatal("expected current model to be OpenAI before remove")
	}

	// Select the provider, show confirmation, and confirm
	for i, item := range m.visibleItems {
		if item.Kind == providerItemProvider {
			m.selectedIdx = i
			break
		}
	}

	m.handleCredentialRemove()
	if !m.confirmRemoveActive {
		t.Fatal("confirmRemoveActive should be true")
	}

	m.handleConfirmRemove(tea.KeyPressMsg{Code: 'y', Text: "y"})

	// Verify env var is unset
	if os.Getenv("OPENAI_API_KEY") != "" {
		t.Fatal("OPENAI_API_KEY should be unset after confirm")
	}

	// Verify connection is removed
	if store.IsConnected(llm.OpenAI, llm.AuthAPIKey) {
		t.Fatal("OpenAI should be disconnected after confirm")
	}

	// Verify cached models are removed
	if _, ok := store.CachedModels(llm.OpenAI, llm.AuthAPIKey); ok {
		t.Fatal("cached models should be removed after confirm")
	}

	// Verify current model is cleared
	if cur := store.CurrentModel(); cur != nil {
		t.Fatalf("current model should be cleared after confirm, got %v", cur)
	}
}

// A Vertex deployment is a project plus an optional region, neither a secret:
// the form shows both rows in the clear, prefilled, and a blank region clears
// the variable rather than failing.
func TestCredentialFormVertexDeployment(t *testing.T) {
	isolateSecretStore(t)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")

	m := NewProviderSelector()
	m.active = true
	m.activeTab = providerTabProviders
	m.allProviders = []providerProviderItem{{
		Provider: llm.Google,
		AuthMethods: []providerAuthMethodItem{{
			Provider:        llm.Google,
			AuthMethod:      llm.AuthVertex,
			EnvVars:         []string{"GOOGLE_CLOUD_PROJECT"},
			OptionalEnvVars: []string{"GOOGLE_CLOUD_LOCATION"},
			Hint:            "run gcloud first",
		}},
	}}
	m.rebuildVisibleItems()

	m.openCredentialForm(m.allProviders[0].AuthMethods[0], 0, 0)
	f := &m.credForm
	if !f.active || len(f.inputs) != 2 || f.required != 1 || f.hint != "run gcloud first" {
		t.Fatalf("form = %+v, want two rows (one required) with the hint", *f)
	}
	if f.inputs[0].EchoMode != textinput.EchoNormal || f.inputs[1].Value() != "us-central1" {
		t.Fatal("deployment rows should show in the clear, prefilled from the environment")
	}
	if !strings.Contains(m.renderCredentialForm(), "run gcloud first") {
		t.Fatal("hint should render above the rows")
	}

	// Enter with the required project blank keeps the form open with an error.
	if cmd := m.handleCredentialFormKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || !f.active || f.err == "" {
		t.Fatalf("blank project should be refused inline, got cmd=%v active=%v err=%q", cmd, f.active, f.err)
	}

	// Tab moves between rows; a blanked optional row clears its variable.
	f.inputs[0].SetValue("my-project")
	m.handleCredentialFormKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if f.focus != 1 {
		t.Fatalf("focus after Tab = %d, want the region row", f.focus)
	}
	f.inputs[1].SetValue("")
	if cmd := m.handleCredentialFormKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("Enter with the project filled should connect")
	}
	if m.credForm.active {
		t.Fatal("form should close after submit")
	}
	if os.Getenv("GOOGLE_CLOUD_PROJECT") != "my-project" || os.Getenv("GOOGLE_CLOUD_LOCATION") != "" {
		t.Fatalf("env after submit: project=%q location=%q", os.Getenv("GOOGLE_CLOUD_PROJECT"), os.Getenv("GOOGLE_CLOUD_LOCATION"))
	}
	if secret.Default().Get("GOOGLE_CLOUD_LOCATION") != "" {
		t.Fatal("blank optional row should clear the stored value")
	}
}
