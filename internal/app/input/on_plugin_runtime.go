package input

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	coreplugin "github.com/genai-io/san/internal/plugin"
)

// Plugin intent messages each request one async mutating operation. They bridge
// from keybindings / executeAction into UpdatePlugin, where deps (cwd, reload)
// and the spinner live.
type pluginEnableMsg struct{ PluginName string }
type pluginDisableMsg struct{ PluginName string }
type pluginUninstallMsg struct{ PluginName string }
type pluginMarketplaceRemoveMsg struct{ ID string }

type pluginInstallMsg struct {
	PluginName  string
	Marketplace string
	Scope       coreplugin.Scope
}

// pluginOpResultMsg is the unified outcome of every async mutating operation
// (install / sync / remove / uninstall / enable / disable). One spinner path,
// one result handler.
type pluginOpResultMsg struct {
	okMsg  string // success status, e.g. "Installed foo"
	errCtx string // failure verb + subject, e.g. "install foo"
	err    error
	pop    bool // goBack() on success (leave the detail / options view)
	reload bool // ReloadAfterPluginChange() on success
}

// UpdatePlugin routes plugin management messages.
func UpdatePlugin(deps OverlayDeps, state *PluginSelector, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case pluginEnableMsg:
		reg, name := state.registry, msg.PluginName
		return tea.Batch(
			state.beginLoading("Enabling "+name+"..."),
			runPluginOp(pluginOpResultMsg{okMsg: "Enabled " + name, errCtx: "enable " + name},
				func() error { return reg.Enable(name, coreplugin.ScopeUser) }),
		), true

	case pluginDisableMsg:
		reg, name := state.registry, msg.PluginName
		return tea.Batch(
			state.beginLoading("Disabling "+name+"..."),
			runPluginOp(pluginOpResultMsg{okMsg: "Disabled " + name, errCtx: "disable " + name},
				func() error { return reg.Disable(name, coreplugin.ScopeUser) }),
		), true

	case pluginInstallMsg:
		reg, cwd := state.registry, deps.Cwd
		name, market, scope := msg.PluginName, msg.Marketplace, msg.Scope
		return tea.Batch(
			state.beginLoading("Installing "+name+"..."),
			runPluginOp(pluginOpResultMsg{okMsg: "Installed " + name, errCtx: "install " + name, pop: true, reload: true},
				func() error {
					ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
					defer cancel()
					return coreplugin.Install(ctx, reg, cwd, coreplugin.FormatPluginRef(name, market), scope)
				}),
		), true

	case pluginUninstallMsg:
		inst, name := state.installer, msg.PluginName
		return tea.Batch(
			state.beginLoading("Uninstalling "+name+"..."),
			runPluginOp(pluginOpResultMsg{okMsg: "Uninstalled " + name, errCtx: "uninstall " + name, pop: true, reload: true},
				func() error { return inst.Uninstall(name, coreplugin.ScopeUser) }),
		), true

	case pluginMarketplaceRemoveMsg:
		mgr, id := state.marketplaceManager, msg.ID
		return tea.Batch(
			state.beginLoading("Removing "+id+"..."),
			runPluginOp(pluginOpResultMsg{okMsg: "Removed " + id, errCtx: "remove " + id, pop: true},
				func() error { return mgr.Remove(id) }),
		), true

	case pluginOpResultMsg:
		state.endLoading()
		if msg.err != nil {
			state.setError(fmt.Sprintf("Failed to %s: %v", msg.errCtx, msg.err))
		} else {
			state.setSuccess(msg.okMsg)
			if msg.pop {
				state.goBack()
			}
		}
		state.refreshAfterOp()
		if msg.err == nil && msg.reload {
			_ = deps.ReloadAfterPluginChange()
		}
		return nil, true

	case pluginLoadingTickMsg:
		if !state.isLoading {
			state.loadingTicking = false
			return nil, true
		}
		state.loadingFrame++
		return pluginLoadingTick(), true
	}
	return nil, false
}

type pluginLoadingTickMsg struct{}

func pluginLoadingTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return pluginLoadingTickMsg{}
	})
}

// beginLoading marks the selector as loading with msg and returns a tick cmd to
// drive the spinner. It returns nil if a tick is already in flight.
func (s *PluginSelector) beginLoading(msg string) tea.Cmd {
	s.isLoading = true
	s.loadingMsg = msg
	return s.startLoadingTick()
}

func (s *PluginSelector) startLoadingTick() tea.Cmd {
	if s.loadingTicking {
		return nil
	}
	s.loadingTicking = true
	return pluginLoadingTick()
}

func (s *PluginSelector) endLoading() {
	s.isLoading = false
	s.loadingMsg = ""
}

func runPluginOp(res pluginOpResultMsg, work func() error) tea.Cmd {
	return func() tea.Msg {
		res.err = work()
		return res
	}
}
