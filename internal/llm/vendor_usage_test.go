package llm

import "testing"

func TestChatGPTSubscription(t *testing.T) {
	for _, tt := range []struct {
		current *CurrentModelInfo
		want    bool
	}{
		{nil, false},
		{&CurrentModelInfo{Provider: OpenAI, AuthMethod: AuthSubscription}, true},
		{&CurrentModelInfo{Provider: OpenAI, AuthMethod: AuthAPIKey}, false},
		{&CurrentModelInfo{Provider: Copilot, AuthMethod: AuthSubscription}, false},
		{&CurrentModelInfo{Provider: OpenAI}, false},
	} {
		if got := ChatGPTSubscription(tt.current); got != tt.want {
			t.Errorf("%+v: got %v", tt.current, got)
		}
	}
}
