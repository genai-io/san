package input

import (
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/llm"
)

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

	if !m.apiKeyActive {
		t.Fatal("handleCredentialEdit should activate API key input for connected providers")
	}
	if m.apiKeyEnvVar != "OPENAI_API_KEY" {
		t.Fatalf("got env var %q, want OPENAI_API_KEY", m.apiKeyEnvVar)
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
	if !m.apiKeyActive {
		t.Fatal("handleCredentialEdit should activate API key input")
	}
	if m.apiKeyEnvVar != "BEDROCK_API_KEY" {
		t.Fatalf("got env var %q, want BEDROCK_API_KEY", m.apiKeyEnvVar)
	}
}

func TestHandleCredentialEditUpdatesOnEnter(t *testing.T) {
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
	if !m.apiKeyActive {
		t.Fatal("handleCredentialEdit should activate API key input")
	}
	if m.apiKeyEnvVar != "OPENAI_API_KEY" {
		t.Fatalf("got env var %q, want OPENAI_API_KEY", m.apiKeyEnvVar)
	}

	// Update the API key
	m.apiKeyInput.SetValue("NEW_TEST_KEY")

	// Save and restore env var to avoid leaking into other tests
	origVal, _ := os.LookupEnv("OPENAI_API_KEY")
	defer func() {
		if origVal != "" {
			os.Setenv("OPENAI_API_KEY", origVal)
		} else {
			os.Unsetenv("OPENAI_API_KEY")
		}
	}()

	// Simulate Enter key to submit
	cmd = m.handleAPIKeyInput(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("handleAPIKeyInput should return a command after Enter")
	}

	// Verify API key input is closed after submission
	if m.apiKeyActive {
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
	if m.apiKeyActive {
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
	if !m.apiKeyActive {
		t.Fatal("handleCredentialEdit should activate API key input")
	}

	// Cancel the edit and verify state
	m.handleAPIKeyInput(tea.KeyMsg{Type: tea.KeyEsc})
	if m.apiKeyActive {
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
	cmd := m.HandleKeypress(tea.KeyMsg{Type: tea.KeyCtrlE})
	if cmd != nil {
		t.Fatal("handleCredentialEdit should not return a command for single auth method")
	}

	if !m.apiKeyActive {
		t.Fatal("handleCredentialEdit should activate API key input")
	}
	if m.apiKeyEnvVar != "OPENAI_API_KEY" {
		t.Fatal("incorrect environment variable set for API key input")
	}
}
