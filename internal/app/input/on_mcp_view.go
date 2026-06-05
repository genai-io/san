package input

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/internal/app/kit"
	coremcp "github.com/genai-io/san/internal/mcp"
)

// mcpStatusDisplay returns icon and label for an MCP server status.
func mcpStatusDisplay(status coremcp.ServerStatus) (icon, label string) {
	switch status {
	case coremcp.StatusConnected:
		return "●", "connected"
	case coremcp.StatusConnecting:
		return "◌", "connecting"
	case coremcp.StatusError:
		return "✗", "error"
	default:
		return "○", "disconnected"
	}
}

// mcpStatusIconAndStyle returns the status icon and style for an MCP server status.
func mcpStatusIconAndStyle(status coremcp.ServerStatus) (string, lipgloss.Style) {
	icon, _ := mcpStatusDisplay(status)
	switch status {
	case coremcp.StatusConnected:
		return icon, kit.SelectorStatusConnected()
	case coremcp.StatusConnecting:
		return icon, kit.SelectorStatusReady()
	case coremcp.StatusError:
		return icon, kit.SelectorStatusError()
	default:
		return icon, kit.SelectorStatusNone()
	}
}

// Render renders the MCP selector.
func (s *MCPSelector) Render() string {
	if !s.active {
		return ""
	}

	if s.level == mcpLevelDetail {
		return s.renderDetail()
	}
	return s.renderList()
}

// renderErrorAndFooter appends the error message (if any) and footer hint to the builder.
func (s *MCPSelector) renderErrorAndFooter(sb *strings.Builder, hint string) {
	if s.lastError != "" {
		sb.WriteString(kit.SelectorStatusError().Render("    ! " + s.lastError + "\n"))
	}
	sb.WriteString("\n")
	if s.connecting {
		sb.WriteString(kit.SelectorHintStyle().Render("Connecting... (Esc to cancel)"))
	} else {
		sb.WriteString(kit.SelectorHintStyle().Render(hint))
	}
}

// renderBox wraps content in a centered bordered box.
func (s *MCPSelector) renderBox(content string) string {
	boxWidth := kit.CalculateToolBoxWidth(s.width)
	box := kit.SelectorBorderStyle().Width(boxWidth).Render(content)
	return lipgloss.Place(s.width, s.height-4, lipgloss.Center, lipgloss.Center, box)
}

// renderList renders the list view.
func (s *MCPSelector) renderList() string {
	var sb strings.Builder
	descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)

	title := fmt.Sprintf("MCP Servers (%d/%d)", len(s.filteredServers), len(s.servers))
	sb.WriteString(kit.SelectorTitleStyle().Render(title))
	sb.WriteString("\n")

	searchPrompt := ">> "
	if s.nav.Search == "" {
		sb.WriteString(kit.SelectorHintStyle().Render(searchPrompt + "Type to filter..."))
	} else {
		sb.WriteString(kit.SelectorBreadcrumbStyle().Render(searchPrompt + s.nav.Search + "|"))
	}
	sb.WriteString("\n\n")

	if len(s.filteredServers) == 0 {
		if len(s.servers) == 0 {
			sb.WriteString(kit.SelectorHintStyle().Render("  No MCP servers configured\n\n"))
			sb.WriteString(kit.SelectorHintStyle().Render("  Add servers with:\n"))
			sb.WriteString(kit.SelectorHintStyle().Render("    san mcp add <name> -- <command>\n"))
		} else {
			sb.WriteString(kit.SelectorHintStyle().Render("  No servers match the filter"))
			sb.WriteString("\n")
		}
	} else {
		endIdx := min(s.nav.Scroll+s.nav.MaxVisible, len(s.filteredServers))

		if s.nav.Scroll > 0 {
			sb.WriteString(kit.SelectorHintStyle().Render("  ^ more above"))
			sb.WriteString("\n")
		}

		for i := s.nav.Scroll; i < endIdx; i++ {
			srv := s.filteredServers[i]
			icon, statusStyle := mcpStatusIconAndStyle(srv.Status)

			nameStyle := descStyle
			if srv.Status == coremcp.StatusConnected {
				nameStyle = statusStyle
			}

			details := s.serverDetails(srv)
			line := fmt.Sprintf("%s %-20s %s  %s",
				statusStyle.Render(icon),
				nameStyle.Render(srv.Name),
				descStyle.Render(fmt.Sprintf("[%s]", srv.Type)),
				descStyle.Render(details),
			)

			if i == s.nav.Selected {
				sb.WriteString(kit.SelectorSelectedStyle().Render("> " + line))
			} else {
				sb.WriteString(kit.SelectorItemStyle().Render("  " + line))
			}
			sb.WriteString("\n")
		}

		if endIdx < len(s.filteredServers) {
			sb.WriteString(kit.SelectorHintStyle().Render("  v more below"))
			sb.WriteString("\n")
		}
	}

	s.renderErrorAndFooter(&sb, "↑↓ navigate . Enter details . ^N add . ^D remove . Esc close")
	return s.renderBox(sb.String())
}

// renderDetail renders the detail view for a selected server.
func (s *MCPSelector) renderDetail() string {
	if s.detailServer == nil {
		return s.renderList()
	}

	var sb strings.Builder
	boxWidth := kit.CalculateToolBoxWidth(s.width)
	srv := s.detailServer
	maxValueLen := boxWidth - 20

	labelStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	valueStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright)

	sb.WriteString(kit.SelectorTitleStyle().Render("MCP Server"))
	sb.WriteString("\n")

	sb.WriteString(kit.SelectorBreadcrumbStyle().Render("> " + srv.Name))
	sb.WriteString("\n\n")

	icon, statusStyle := mcpStatusIconAndStyle(srv.Status)
	_, statusLabel := mcpStatusDisplay(srv.Status)
	fmt.Fprintf(&sb, "  %s  %s\n",
		labelStyle.Render("Status:"),
		statusStyle.Render(icon+" "+statusLabel),
	)

	fmt.Fprintf(&sb, "  %s  %s\n",
		labelStyle.Render("Type:  "),
		valueStyle.Render(srv.Type),
	)

	if srv.Scope != "" {
		fmt.Fprintf(&sb, "  %s  %s\n",
			labelStyle.Render("Scope: "),
			valueStyle.Render(srv.Scope),
		)
	}

	if srv.URL != "" {
		fmt.Fprintf(&sb, "  %s  %s\n",
			labelStyle.Render("URL:   "),
			valueStyle.Render(kit.TruncateText(srv.URL, maxValueLen)),
		)
	}
	if srv.Command != "" {
		cmd := srv.Command
		if len(srv.Args) > 0 {
			cmd += " " + strings.Join(srv.Args, " ")
		}
		fmt.Fprintf(&sb, "  %s  %s\n",
			labelStyle.Render("Cmd:   "),
			valueStyle.Render(kit.TruncateText(cmd, maxValueLen)),
		)
	}

	if srv.Status == coremcp.StatusConnected {
		fmt.Fprintf(&sb, "  %s  %s\n",
			labelStyle.Render("Tools: "),
			valueStyle.Render(fmt.Sprintf("%d", srv.ToolCount)),
		)
	}

	if srv.Error != "" {
		fmt.Fprintf(&sb, "  %s  %s\n",
			labelStyle.Render("Error: "),
			kit.SelectorStatusError().Render(srv.Error),
		)
	}

	sb.WriteString("\n")
	sb.WriteString(labelStyle.Render("  Actions:"))
	sb.WriteString("\n")
	for i, action := range s.actions {
		if i == s.actionIdx {
			sb.WriteString(kit.SelectorSelectedStyle().Render("> " + action.Label))
		} else {
			sb.WriteString(kit.SelectorItemStyle().Render("  " + action.Label))
		}
		sb.WriteString("\n")
	}

	s.renderErrorAndFooter(&sb, "↑↓ navigate . Enter execute . Esc back")
	return s.renderBox(sb.String())
}

// serverDetails returns the details string for a server item.
func (s *MCPSelector) serverDetails(srv mcpServerItem) string {
	if srv.Status == coremcp.StatusConnected {
		return fmt.Sprintf("Tools: %d", srv.ToolCount)
	}
	if srv.Error != "" {
		if len(srv.Error) > 30 {
			return srv.Error[:27] + "..."
		}
		return srv.Error
	}
	return ""
}
