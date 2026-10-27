package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai/auth"

	"github.com/genai-io/san/internal/secret"
)

func isolateCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_CHATGPT_AUTH", "")
	t.Setenv("GITHUB_COPILOT_AUTH", "")
	secret.ResetDefault()
	t.Cleanup(secret.ResetDefault)
}

func TestCredentialsReloadRotatedTokens(t *testing.T) {
	isolateCredentials(t)
	store := secret.Default()
	old := codexBlob{AccessToken: "old", RefreshToken: "spent"}
	raw, _ := json.Marshal(old)
	if err := store.Set("OPENAI_CHATGPT_AUTH", string(raw)); err != nil {
		t.Fatal(err)
	}
	// A different process replaces the disk record, leaving this process's
	// secret-store cache unchanged.
	fresh := codexBlob{AccessToken: "fresh", RefreshToken: "r2", AccountID: "account", IDToken: "id"}
	raw, _ = json.Marshal(map[string]string{"OPENAI_CHATGPT_AUTH": credentialJSON(t, fresh)})
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".san", "secrets.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	c, found, err := (credentials{}).Load(codexVendor)
	if err != nil || !found || c.Refresh != "r2" {
		t.Fatalf("Load: found=%v err=%v; want the rotated credential", found, err)
	}
	if err := (credentials{}).Save(auth.Credential{Vendor: codexVendor, Access: "next", Refresh: "r3"}); err != nil {
		t.Fatal(err)
	}
	var saved codexBlob
	if err := json.Unmarshal([]byte(store.Get("OPENAI_CHATGPT_AUTH")), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AccountID != "account" || saved.IDToken != "id" {
		t.Fatal("saving a rotation lost metadata written by another process")
	}
}

func credentialJSON(t *testing.T, c codexBlob) string {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEnvironmentCredentialBootstrapsButDoesNotShadowRotation(t *testing.T) {
	isolateCredentials(t)
	t.Setenv("OPENAI_CHATGPT_AUTH", credentialJSON(t, codexBlob{AccessToken: "old", RefreshToken: "r1"}))
	c, found, err := (credentials{}).Load(codexVendor)
	if err != nil || !found || c.Refresh != "r1" {
		t.Fatal("environment credential did not bootstrap an empty store")
	}
	if err := (credentials{}).Save(auth.Credential{Vendor: codexVendor, Access: "fresh", Refresh: "r2", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c, found, err = (credentials{}).Load(codexVendor)
	if err != nil || !found || c.Refresh != "r2" {
		t.Fatal("environment credential shadowed the saved rotation")
	}
	if err := (credentials{}).Delete(codexVendor); err != nil {
		t.Fatal(err)
	}
	if _, found, err := (credentials{}).Load(codexVendor); err != nil || found {
		t.Fatal("sign-out left an environment credential active")
	}
}

func TestCredentialsReportUnreadableStore(t *testing.T) {
	isolateCredentials(t)
	_ = secret.Default()
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".san", "secrets.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (credentials{}).Load(codexVendor); err == nil {
		t.Fatal("credential load hid a store read failure")
	}
}
