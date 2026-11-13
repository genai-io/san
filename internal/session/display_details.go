package session

import (
	"encoding/json"

	"github.com/genai-io/san/internal/tool/toolresult"
)

// Details retain their native JSON shape; known display records are decoded
// here, while future tool-specific metadata remains available as raw JSON.
func displayToolDetails(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return json.RawMessage(raw)
	}
	if _, ok := fields["Path"]; ok {
		var d toolresult.FileChangeDetails
		if json.Unmarshal(raw, &d) == nil {
			return d
		}
	}
	if _, ok := fields["Identity"]; ok {
		var d toolresult.AgentDetails
		if json.Unmarshal(raw, &d) == nil {
			return d
		}
	}
	if _, ok := fields["lineCount"]; ok {
		var d toolresult.BashDetails
		if json.Unmarshal(raw, &d) == nil {
			return d
		}
	}
	return json.RawMessage(raw)
}
