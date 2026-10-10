package cron

import (
	"testing"
	"time"
)

func TestNextAfterDSTTransitions(t *testing.T) {
	cases := []struct{ name, zone, cron, after, want string }{
		{"fall weekly", "America/New_York", "0 9 * * 1", "2026-10-31T23:59:00-04:00", "2026-11-02T09:00:00-05:00"},
		{"fall hour", "America/New_York", "0 2 * * *", "2026-11-01T00:59:00-04:00", "2026-11-01T02:00:00-05:00"},
		{"fall repeated minute", "America/New_York", "30 1 * * *", "2026-11-01T01:31:00-04:00", "2026-11-01T01:30:00-05:00"},
		{"spring missing hour", "America/New_York", "30 2 * * *", "2026-03-08T01:59:00-05:00", "2026-03-09T02:30:00-04:00"},
		{"half hour repeat", "Australia/Lord_Howe", "45 1 * * *", "2026-04-05T01:46:00+11:00", "2026-04-05T01:45:00+10:30"},
		{"missing midnight", "America/Sao_Paulo", "0 9 * * 0", "2018-11-03T23:00:00-03:00", "2018-11-04T09:00:00-02:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			zone, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			after, err := time.Parse(time.RFC3339, tc.after)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			expr, err := parse(tc.cron)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan time.Time, 1)
			go func() { done <- expr.nextAfter(after.In(zone)) }()
			select {
			case got := <-done:
				if !got.Equal(want) {
					t.Fatalf("next = %s, want %s", got, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("nextAfter did not advance across DST")
			}
		})
	}
}
