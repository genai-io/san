package fs

import "github.com/genai-io/san/internal/tool"

// BashPromptResponder decides how to answer an interactive prompt a bash command
// raises while it runs. It is consulted only in auto-review mode.
//
// The two methods keep a hard security boundary: a secret (password/passphrase)
// is handled by RequestSecret and its value must go straight to the process —
// it is never passed to AnswerPrompt, a model, a log, or the transcript.
type BashPromptResponder = tool.BashPromptResponder
