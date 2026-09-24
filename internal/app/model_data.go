package app

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
)

// refreshModelData keeps the model lineups, limits and prices current, off
// the startup path; what it fetches applies from the next start.
// SAN_DISABLE_MODEL_REFRESH turns it off for an offline machine.
func refreshModelData() tea.Cmd {
	return func() tea.Msg {
		if setting.Getenv("DISABLE_MODEL_REFRESH") != "" {
			return nil
		}
		if err := llm.RefreshModelData(context.Background()); err != nil {
			log.Logger().Debug("model data refresh failed", zap.Error(err))
		}
		return nil
	}
}
