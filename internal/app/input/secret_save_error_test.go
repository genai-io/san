package input

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/llm"
)

// Each credential form must stay open with an error when the secret store
// cannot be written, instead of reporting success.
func TestCredentialFormsReportSecretSaveFailure(t *testing.T) {
	breakStore := func(t *testing.T) {
		isolateSecretStore(t)
		// A directory where secrets.json belongs makes every write fail.
		if err := os.MkdirAll(filepath.Join(confdir.Dir(os.Getenv("HOME")), "secrets.json"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("provider credential", func(t *testing.T) {
		breakStore(t)
		t.Setenv("OPENAI_API_KEY", "")
		m := NewProviderSelector()
		m.openCredentialForm(providerAuthMethodItem{Provider: llm.OpenAI, AuthMethod: llm.AuthAPIKey, EnvVars: []string{"OPENAI_API_KEY"}}, -1, -1)
		m.credForm.inputs[0].SetValue("k")
		m.submitCredentialForm()
		if !m.credForm.active || m.credForm.err == "" || os.Getenv("OPENAI_API_KEY") != "" {
			t.Fatalf("active=%v err=%q env=%q", m.credForm.active, m.credForm.err, os.Getenv("OPENAI_API_KEY"))
		}
	})

	t.Run("custom provider", func(t *testing.T) {
		m := NewProviderSelector()
		m.store = newProviderTestStore(t)
		breakStore(t)
		t.Setenv(llm.CustomAPIKeyEnvVar, "")
		m.openCustomForm()
		m.customFormInputs[customFormFieldBaseURL].SetValue("https://x.example/v1")
		m.customFormInputs[customFormFieldAPIKey].SetValue("k")
		m.submitCustomForm()
		if !m.customFormActive || m.customFormErr == "" {
			t.Fatalf("active=%v err=%q", m.customFormActive, m.customFormErr)
		}
	})

	t.Run("ollama", func(t *testing.T) {
		breakStore(t)
		t.Setenv(ollamaEnvVar, "")
		m := NewProviderSelector()
		m.openOllamaForm()
		m.ollamaURLInput.SetValue("http://localhost:11434/v1")
		m.submitOllamaForm()
		if !m.ollamaFormActive || m.ollamaFormErr == "" {
			t.Fatalf("active=%v err=%q", m.ollamaFormActive, m.ollamaFormErr)
		}
	})

	t.Run("search key", func(t *testing.T) {
		breakStore(t)
		t.Setenv("TAVILY_API_KEY", "")
		s := SearchSelector{items: []searchItem{{EnvVars: []string{"TAVILY_API_KEY"}}}}
		s.openAPIKeyInput()
		s.apiKeyInput.SetValue("k")
		s.handleAPIKeyInput(tea.KeyPressMsg{Code: tea.KeyEnter})
		if !s.apiKeyActive || s.apiKeyErr == "" {
			t.Fatalf("active=%v err=%q", s.apiKeyActive, s.apiKeyErr)
		}
	})
}
