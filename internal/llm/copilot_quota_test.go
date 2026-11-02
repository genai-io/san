package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCopilotQuota(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		fail       bool
	}{
		{"free", `{"quota_snapshots":{"chat":{"entitlement":200,"remaining":200,"quota_remaining":200,"percent_remaining":100,"has_quota":true},"completions":{"entitlement":2000,"remaining":2000},"premium_interactions":{"entitlement":0,"remaining":0,"has_quota":false}},"quota_reset_date_utc":"2026-12-01T00:00:00Z","quota_reset_date":"2026-12-01"}`, 200, false},
		{"unlimited", `{"quota_snapshots":{"chat":{"unlimited":true}}}`, 200, false},
		{"unauthorized", `sensitive response must not escape`, 401, true},
		{"malformed", `{`, 200, true},
		{"missing", `{}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "token test-token" || r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "GitHubCopilotChat/0.26.7" {
					t.Error("incorrect request")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			q, err := fetchCopilotQuota(context.Background(), server.Client(), server.URL, "test-token")
			if (err != nil) != tc.fail {
				t.Fatalf("error = %v", err)
			}
			if tc.name == "free" {
				if *q.Snapshots["chat"].Remaining != 200 || *q.Snapshots["chat"].PercentRemaining != 100 || *q.Snapshots["premium_interactions"].HasQuota || q.ResetDate != "2026-12-01" {
					t.Fatalf("wrong quota: %+v", q)
				}
			}
			if tc.name == "unlimited" && !q.Snapshots["chat"].Unlimited {
				t.Fatal("lost unlimited")
			}
		})
	}
}

func TestCopilotQuotaCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("canceled request reached server") }))
	defer server.Close()
	if _, err := fetchCopilotQuota(ctx, server.Client(), server.URL, "test-token"); err == nil {
		t.Fatal("expected cancellation failure")
	}
}
