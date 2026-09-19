package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/autoupdate"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
)

// updateInstalledMsg carries the release the background auto-update swapped in
// on disk. The running session is unaffected; the next launch runs it.
type updateInstalledMsg string

// autoUpdate is the startup command that keeps an installer-managed binary
// current. Failures never interrupt the session; SAN_DISABLE_AUTOUPDATE turns
// it off.
func autoUpdate(current string) tea.Cmd {
	// Everything decidable without the network is decided before it: a binary
	// outside the installer's dir is never ours to replace, and a dev build
	// could never pass Newer.
	if setting.Getenv("DISABLE_AUTOUPDATE") != "" || !autoupdate.Managed() || !autoupdate.IsRelease(current) {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		latest, err := autoupdate.Latest(ctx)
		if err != nil || !autoupdate.Newer(latest, current) {
			return nil
		}
		if err := autoupdate.Install(ctx, latest, nil); err != nil {
			log.Logger().Debug("background auto-update skipped", zap.String("release", latest), zap.Error(err))
			return nil
		}
		log.Logger().Info("background auto-update installed", zap.String("from", current), zap.String("to", latest))
		return updateInstalledMsg(latest)
	}
}
