// Mouse event handling: scroll wheel navigation within the chat content area.
package app

import tea "github.com/charmbracelet/bubbletea"

const mouseWheelScrollDelta = 3

// handleMouseEvent processes tea.MouseMsg events. Wheel up/down adjusts the
// content scroll offset so the user can scroll through the chat output with
// the mouse wheel. Other mouse events (clicks, motion) pass through unchanged.
func (m *model) handleMouseEvent(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.conv.ContentOffset += mouseWheelScrollDelta
	case tea.MouseButtonWheelDown:
		if m.conv.ContentOffset > 0 {
			m.conv.ContentOffset -= mouseWheelScrollDelta
			if m.conv.ContentOffset < 0 {
				m.conv.ContentOffset = 0
			}
		}
	}
	return nil
}
