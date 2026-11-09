package session

import (
	"github.com/genai-io/san/internal/transcript"
)

type LegacyMetadata = transcript.Metadata

// LegacySummary contains the fields needed to list a session without loading its messages.
type LegacySummary = transcript.Summary
