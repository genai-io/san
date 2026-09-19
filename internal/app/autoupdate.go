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

// updateFailedMsg reports a release that exists but could not be installed —
// the one outcome worth a word to the user, printed at exit so it never
// interrupts the session. A failed version check is not reported: nothing was
// there to install, and an offline launch should not nag on every exit.
type updateFailedMsg struct {
	version string
	err     error
}

// autoUpdate is the startup command that keeps an installer-managed binary
// current. It costs the startup path nothing: every check, including the
// cleanup of an earlier update's leftovers, runs on the command's own
// goroutine. Nothing it does can reach the session — failures become a log
// line or an exit-time warning, and a panic is swallowed here because Bubble
// Tea would otherwise tear the whole program down for it.
// SAN_DISABLE_AUTOUPDATE turns the check off; the cleanup still runs.
func autoUpdate(current string) tea.Cmd {
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				log.Logger().Warn("background auto-update panicked", zap.Any("panic", r))
				msg = nil
			}
		}()
		autoupdate.Cleanup()

		// Everything decidable without the network is decided before it: a
		// binary outside the installer's dir is never ours to replace, and a
		// dev build could never pass Newer.
		if setting.Getenv("DISABLE_AUTOUPDATE") != "" || !autoupdate.Managed() || !autoupdate.IsRelease(current) {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		latest, err := autoupdate.Latest(ctx)
		if err != nil {
			log.Logger().Debug("background auto-update check failed", zap.Error(err))
			return nil
		}
		if !autoupdate.Newer(latest, current) {
			return nil
		}
		if err := autoupdate.Install(ctx, latest, nil); err != nil {
			log.Logger().Warn("background auto-update failed", zap.String("release", latest), zap.Error(err))
			return updateFailedMsg{version: latest, err: err}
		}
		log.Logger().Info("background auto-update installed", zap.String("from", current), zap.String("to", latest))
		return updateInstalledMsg(latest)
	}
}
