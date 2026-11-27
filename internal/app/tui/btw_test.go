package tui

import "testing"

func TestBtwAnswerAfterSessionChangeIsDropped(t *testing.T) {
	m := &model{features: appFeatures{sessionID: "new"}}
	ask := btwAsk{question: "why?", sessionID: "old"}
	m.btw = []btwAsk{ask}

	m.onBtwAnswer(btwAnswerMsg{ask: ask, answer: "because"})
	if len(m.btw) != 0 {
		t.Fatalf("answered ask must leave the live rows: %+v", m.btw)
	}
	if len(m.conv.Messages) != 0 {
		t.Fatalf("answer to another session's question must not land here: %+v", m.conv.Messages)
	}
}
