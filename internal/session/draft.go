package session

import "context"

func (s *Session) appendDraft(ctx context.Context, events []Event, prepared *preparedEventBatch) error {
	for _, e := range events {
		if !draftMetadata(e) {
			return s.publishDraft(ctx, prepared.projection, prepared.validation)
		}
	}
	s.writer.mu.Lock()
	s.writer.state.projection, s.writer.state.validation = prepared.projection, prepared.validation
	s.writer.mu.Unlock()
	return nil
}

func draftMetadata(e Event) bool {
	if e.Source != nil {
		return false
	}
	switch p := e.Payload.(type) {
	case *SessionUpdatePayload:
		// Group membership needs a saved identity even before the first message.
		return p.State == nil || p.State.GroupHint.Value == nil
	case *AnnotationAddPayload, *InstructionsUpdatePayload, *ToolsetUpdatePayload, *SkillsUpdatePayload:
		return true
	default:
		return false
	}
}

// Save publishes a draft before an operation that needs a durable session ID
// or directory, such as forking or writing a named asset. It is idempotent.
func (s *Session) Save(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writer.Err(); err != nil {
		return err
	}
	if err := s.store.checkID(ctx, s.ID()); err != nil {
		return err
	}
	if !s.draft {
		return nil
	}
	return (library{root: s.store.projectDir}).withLock(ctx, s.ID(), func() error {
		return s.saveDraftLocked(ctx)
	})
}

func (s *Session) saveDraftLocked(ctx context.Context) error {
	return s.publishDraft(ctx, s.writer.state.projection, s.writer.state.validation)
}

func (s *Session) publishDraft(ctx context.Context, p *Projection, validation EventValidationContext) error {
	// Publish the buffered prefix and its first durable event in one batch.
	opened := &eventSession{id: s.ID(), path: s.Path(), state: newEventState()}
	prepared, err := opened.prepareEventBatch("create", p.data.Events, validation)
	if err != nil {
		return err
	}
	opened, err = s.store.publishBatch(ctx, prepared)
	if err != nil {
		return err
	}
	s.writer.mu.Lock()
	s.writer.log, s.writer.state = opened.log, opened.state
	s.writer.mu.Unlock()
	s.draft = false
	return nil
}
