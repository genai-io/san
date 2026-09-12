package transcript

import "time"

type MetadataView struct {
	ID              string
	Title           string
	LastPrompt      string
	Tag             string
	Mode            string
	AutoPilot       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Provider        string
	Model           string
	Cwd             string
	MessageCount    int
	ParentSessionID string
}

func MetadataFromTranscript(t *Transcript) MetadataView {
	if t == nil {
		return MetadataView{}
	}
	return MetadataView{
		ID:              t.ID,
		Title:           t.State.Title,
		LastPrompt:      t.State.LastPrompt,
		Tag:             t.State.Tag,
		Mode:            t.State.Mode,
		AutoPilot:       t.State.AutoPilot,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		Provider:        t.Provider,
		Model:           t.Model,
		Cwd:             t.Cwd,
		MessageCount:    len(t.Messages),
		ParentSessionID: t.ParentID,
	}
}

func MetadataFromListItem(item ListItem, cwd string) MetadataView {
	return MetadataView{
		ID:           item.SessionID,
		Title:        item.Title,
		LastPrompt:   item.LastPrompt,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
		Cwd:          cwd,
		MessageCount: item.MessageCount,
	}
}
