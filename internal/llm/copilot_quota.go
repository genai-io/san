package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CopilotQuotaSnapshot is a provider-reported allowance, not a monetary cost
// or a count of requests. Pointers preserve missing fields as unknown.
type CopilotQuotaSnapshot struct {
	Entitlement       *float64 `json:"entitlement"`
	Remaining         *float64 `json:"remaining"`
	QuotaRemaining    *float64 `json:"quota_remaining"`
	PercentRemaining  *float64 `json:"percent_remaining"`
	Unlimited         bool     `json:"unlimited"`
	HasQuota          *bool    `json:"has_quota"`
	CreditsUsed       *float64 `json:"credits_used"`
	TokenBasedBilling bool     `json:"token_based_billing"`
	OveragePermitted  bool     `json:"overage_permitted"`
}

// CopilotQuota retains the account's separate quota buckets and reset date.
type CopilotQuota struct {
	Snapshots    map[string]CopilotQuotaSnapshot `json:"quota_snapshots"`
	ResetDateUTC string                          `json:"quota_reset_date_utc"`
	ResetDate    string                          `json:"quota_reset_date"`
}

// FetchCopilotQuota uses the durable GitHub OAuth credential, never the exchanged
// inference bearer. It is read-only and must be called off the UI goroutine.
func FetchCopilotQuota(ctx context.Context) (CopilotQuota, error) {
	credential, found, err := (credentials{}).Load(copilotVendor)
	if err != nil || !found {
		return CopilotQuota{}, errors.New("Copilot quota credentials unavailable")
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return fetchCopilotQuota(ctx, client, "https://api.github.com/copilot_internal/user", credential.Access)
}

func fetchCopilotQuota(ctx context.Context, client *http.Client, endpoint, token string) (CopilotQuota, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return CopilotQuota{}, errors.New("Copilot quota request unavailable")
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")
	resp, err := client.Do(req)
	if err != nil {
		return CopilotQuota{}, errors.New("Copilot quota unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CopilotQuota{}, fmt.Errorf("Copilot quota HTTP %d", resp.StatusCode)
	}
	var quota CopilotQuota
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&quota); err != nil || len(quota.Snapshots) == 0 {
		return CopilotQuota{}, errors.New("Copilot quota response unavailable")
	}
	return quota, nil
}
