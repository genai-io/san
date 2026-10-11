// Optional repository status polling. All Git subprocesses run in tea.Cmd,
// never on the UI goroutine or from View. Generation-tagged results and ticks
// cannot restore an old workspace or restart polling after the bar is disabled.
package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/proc"
)

const (
	gitBarPollInterval = 3 * time.Second
	gitBarReadTimeout  = 2 * time.Second
)

type gitBarState struct {
	cwd        string // empty when polling is disabled
	generation uint64
	cancel     context.CancelFunc // non-nil until the read result is handled
	status     proc.GitStatus
}

type gitStatusMsg struct {
	generation uint64
	cwd        string
	status     proc.GitStatus
}

type gitBarTickMsg struct{ generation uint64 }

// syncGitBar starts polling only on enable or workspace change. With the
// setting off, no timer or Git command is started.
func (m *model) syncGitBar() tea.Cmd {
	if !m.env.ShowGitBar || m.env.CWD == "" {
		if m.gitBar.cwd != "" {
			m.cancelGitBarRead()
			m.gitBar = gitBarState{generation: m.gitBar.generation + 1}
		}
		return nil
	}
	if m.gitBar.cwd == m.env.CWD {
		return nil
	}
	m.cancelGitBarRead()
	m.gitBar = gitBarState{
		cwd:        m.env.CWD,
		generation: m.gitBar.generation,
	}
	return m.refreshGitBar()
}

func (m *model) cancelGitBarRead() {
	if m.gitBar.cancel != nil {
		m.gitBar.cancel()
		m.gitBar.cancel = nil
	}
}

func (m *model) refreshGitBar() tea.Cmd {
	if m.gitBar.cancel != nil {
		return nil
	}
	m.gitBar.generation++
	generation, cwd := m.gitBar.generation, m.gitBar.cwd
	ctx, cancel := context.WithTimeout(context.Background(), gitBarReadTimeout)
	m.gitBar.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		// Missing Git, non-repositories and failed reads simply hide the row.
		// Do not retain a stale snapshot or misreport an unavailable read as clean.
		status, _ := proc.ReadGitStatus(ctx, cwd)
		return gitStatusMsg{generation: generation, cwd: cwd, status: status}
	}
}

func (m *model) handleGitStatus(msg gitStatusMsg) tea.Cmd {
	if !m.env.ShowGitBar || m.gitBar.cwd == "" || msg.generation != m.gitBar.generation || msg.cwd != m.env.CWD {
		return nil
	}
	m.cancelGitBarRead()
	m.gitBar.status = msg.status
	return tea.Tick(gitBarPollInterval, func(time.Time) tea.Msg {
		return gitBarTickMsg{generation: msg.generation}
	})
}

func (m *model) handleGitBarTick(msg gitBarTickMsg) tea.Cmd {
	if !m.env.ShowGitBar || m.gitBar.cwd == "" || msg.generation != m.gitBar.generation || m.gitBar.cwd != m.env.CWD {
		return nil
	}
	return m.refreshGitBar()
}

func (m *model) renderGitBar() string {
	if !m.env.ShowGitBar || m.gitBar.cwd != m.env.CWD {
		return ""
	}
	status := m.gitBar.status
	return conv.RenderGitBar(conv.GitBarParams{
		Width:      m.env.Width,
		Branch:     status.Branch,
		Repository: status.Repository,
		Staged:     status.Staged,
		Modified:   status.Modified,
		Untracked:  status.Untracked,
		Conflicts:  status.Conflicts,
		Ahead:      status.Ahead,
		Behind:     status.Behind,
	})
}
