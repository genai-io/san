package inspector

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func getSessionPage(t *testing.T, srv *httptest.Server, query string) sessionPage {
	t.Helper()
	var page sessionPage
	getJSON(t, srv, "/api/sessions"+query, &page)
	return page
}

func TestSessionPagesOrderAndTitleBudget(t *testing.T) {
	store, first, srv := fixture(t)
	ctx := t.Context()
	appendPayload(t, first, session.SessionUpdatePayload{State: &session.StatePatch{Title: session.SetField("small session title")}})
	newest := time.Now()
	for _, id := range []string{"a-large", "b-renamed", "c-tied"} {
		w, err := store.Create(ctx, session.CreateOptions{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if id != "c-tied" {
			appendPayload(t, w, session.MessageAppendPayload{Messages: []ai.Message{message("large", strings.Repeat("x", sessionTitleBudget))}})
			appendPayload(t, w, session.SessionUpdatePayload{State: &session.StatePatch{Title: session.SetField("generated from large log")}})
		}
		if id == "b-renamed" {
			if err := store.SetAttrs(ctx, id, session.AttrsPatch{Title: session.SetField("renamed")}); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chtimes(w.Path(), newest, newest); err != nil {
			t.Fatal(err)
		}
	}
	older := newest.Add(-time.Hour)
	if err := os.Chtimes(first.Path(), older, older); err != nil {
		t.Fatal(err)
	}
	before := diskSnapshot(t, store.ProjectDir())
	a := getSessionPage(t, srv, "?limit=2")
	if len(a.Items) != 2 || a.Items[0].ID != "a-large" || a.Items[0].Title != "" || a.Items[1].ID != "b-renamed" || a.Items[1].Title != "renamed" || a.Next == "" {
		t.Fatalf("unexpected first page: %+v", a)
	}
	b := getSessionPage(t, srv, "?limit=2&cursor="+url.QueryEscape(a.Next))
	if len(b.Items) != 2 || b.Items[0].ID != "c-tied" || b.Items[1].ID != first.ID() || b.Items[1].Title != "small session title" || b.Next != "" {
		t.Fatalf("unexpected last page: %+v", b)
	}
	if !reflect.DeepEqual(before, diskSnapshot(t, store.ProjectDir())) {
		t.Fatal("pagination modified session files")
	}
}

func TestSessionPageRejectsInvalidBounds(t *testing.T) {
	_, _, srv := fixture(t)
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=bad", "?cursor=bad!"} {
		resp, err := srv.Client().Get(srv.URL + "/api/sessions" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: got status %d", query, resp.StatusCode)
		}
	}
}
