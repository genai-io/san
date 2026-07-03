package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/secret"
)

// makeJWT builds an unsigned JWT with the given claims for testing the
// (verification-free) claim decoders.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}

func TestAccountID(t *testing.T) {
	idToken := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-123",
		},
	})

	if got := accountID(idToken); got != "acct-123" {
		t.Errorf("accountID = %q, want acct-123", got)
	}

	// Falls through to the second token when the first lacks the claim.
	plain := makeJWT(t, map[string]any{"sub": "user"})
	if got := accountID(plain, idToken); got != "acct-123" {
		t.Errorf("accountID fallback = %q, want acct-123", got)
	}

	// No account id anywhere → empty.
	if got := accountID(plain, "not-a-jwt"); got != "" {
		t.Errorf("accountID = %q, want empty", got)
	}
}

func TestJWTExpiry(t *testing.T) {
	want := time.Now().Add(time.Hour).Truncate(time.Second)
	tok := makeJWT(t, map[string]any{"exp": float64(want.Unix())})

	got, ok := jwtExpiry(tok)
	if !ok || !got.Equal(want) {
		t.Errorf("jwtExpiry = %v, %v; want %v, true", got, ok, want)
	}

	if _, ok := jwtExpiry("garbage"); ok {
		t.Error("jwtExpiry on garbage should return ok=false")
	}
}

func TestExpiresAtPrefersJWT(t *testing.T) {
	want := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	tok := makeJWT(t, map[string]any{"exp": float64(want.Unix())})

	// JWT exp wins over the expires_in hint.
	if got := expiresAt(tok, 60); !got.Equal(want) {
		t.Errorf("expiresAt = %v, want %v (from JWT exp)", got, want)
	}

	// No JWT exp → fall back to expires_in.
	got := expiresAt("no-exp", 3600)
	if d := time.Until(got); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("expiresAt fallback = %v (in %v), want ~1h", got, d)
	}
}

func TestNewPKCEChallengeMatchesVerifier(t *testing.T) {
	verifier, challenge, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE: %v", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); challenge != want {
		t.Errorf("challenge = %q, want S256(verifier) = %q", challenge, want)
	}
	if strings.ContainsAny(challenge, "+/=") {
		t.Errorf("challenge %q is not base64url (unpadded)", challenge)
	}
}

func TestAuthorizeEndpoint(t *testing.T) {
	raw := authorizeEndpoint("http://localhost:1455/auth/callback", "chal", "st")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"response_type":         "code",
		"client_id":             ClientID,
		"redirect_uri":          "http://localhost:1455/auth/callback",
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
		"state":                 "st",
		"originator":            originator,
	} {
		if got := q.Get(k); got != want {
			t.Errorf("authorize param %s = %q, want %q", k, got, want)
		}
	}
	if !strings.HasPrefix(raw, authorizeURL+"?") {
		t.Errorf("authorize url %q does not target %q", raw, authorizeURL)
	}
}

func TestStorageRoundTripAndLogout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	secret.ResetDefault()
	t.Cleanup(secret.ResetDefault)

	if HasCredentials() {
		t.Fatal("expected no credentials in a fresh store")
	}

	want := Tokens{
		AccessToken:  "access",
		RefreshToken: "refresh",
		IDToken:      "id",
		AccountID:    "acct-1",
		ExpiresAt:    time.Now().Add(time.Hour).Truncate(time.Second),
	}
	if err := save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !HasCredentials() {
		t.Fatal("HasCredentials should be true after save")
	}

	got, ok := load()
	if !ok || got.AccessToken != want.AccessToken || got.AccountID != want.AccountID || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("load = %+v, %v; want %+v", got, ok, want)
	}

	if err := Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if HasCredentials() {
		t.Error("HasCredentials should be false after Logout")
	}
}

func TestStaleWithinRefreshWindow(t *testing.T) {
	if !(Tokens{ExpiresAt: time.Now().Add(refreshWindow / 2)}).stale() {
		t.Error("token expiring inside the refresh window should be stale")
	}
	if (Tokens{ExpiresAt: time.Now().Add(refreshWindow * 3)}).stale() {
		t.Error("token expiring well beyond the refresh window should not be stale")
	}
}
