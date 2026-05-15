package session

import "time"

func NormalizeMetadata(meta *SessionMetadata, entries []Entry, defaultCwd string, now time.Time) {
	for i := range entries {
		if entries[i].Type == "" && entries[i].Message != nil {
			entries[i].Type = entryTypeForRole(entries[i].Message.Role)
		}
	}
	if meta.ID == "" {
		meta.ID = generateSessionID()
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.UpdatedAt = now
	meta.MessageCount = len(entries)
	if meta.Cwd == "" {
		meta.Cwd = defaultCwd
	}
	if meta.LastPrompt == "" {
		meta.LastPrompt = ExtractLastUserText(entries)
	}
	if meta.Title == "" {
		meta.Title = GenerateTitle(entries)
	}
}
