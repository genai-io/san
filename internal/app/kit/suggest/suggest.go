package suggest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/v2/internal/app/kit"
)

type Type int

const (
	typeCommand Type = iota
	TypeFile
)

type Suggestion struct {
	Name        string
	Description string
}

type Matcher func(query string) []Suggestion

func suggestionBoxStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(kit.CurrentTheme.Border).
		Padding(0, 1)
}

func selectedSuggestionStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.TextBright).
		Bold(true)
}

func normalSuggestionStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.Muted)
}

func commandNameStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.Primary)
}

func commandDescStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.Muted)
}

type fileSuggestion struct {
	Path        string
	DisplayName string
	IsDir       bool
}

type State struct {
	visible         bool
	suggestionType  Type
	suggestions     []Suggestion
	fileSuggestions []fileSuggestion
	selectedIdx     int
	viewStart       int
	cwd             string
	atQuery         string
	cmdMatcher      Matcher

	allFiles    []fileSuggestion
	allFilesCwd string
}

func NewState(matcher Matcher) State {
	return State{
		visible:    false,
		cmdMatcher: matcher,
	}
}

func (s *State) Reset() {
	s.visible = false
	s.suggestions = nil
	s.fileSuggestions = nil
	s.selectedIdx = 0
	s.viewStart = 0
	s.atQuery = ""
}

func (s *State) UpdateSuggestions(input string) {
	input = strings.TrimSpace(input)

	if atIdx := strings.LastIndex(input, "@"); atIdx >= 0 {
		query := input[atIdx+1:]
		if atIdx == len(input)-1 || !strings.ContainsAny(query, " \t\n") {
			if s.atQuery != query {
				s.selectedIdx = 0
				s.viewStart = 0
			}
			s.atQuery = query
			s.updatefileSuggestions(query)
			return
		}
	}

	if strings.HasPrefix(input, "/") {
		s.suggestionType = typeCommand
		s.suggestions = s.cmdMatcher(input)
		s.fileSuggestions = nil
		s.visible = len(s.suggestions) > 0
		s.atQuery = ""

		if s.selectedIdx >= len(s.suggestions) {
			s.selectedIdx = 0
		}
		return
	}

	s.visible = false
	s.suggestions = nil
	s.fileSuggestions = nil
	s.selectedIdx = 0
	s.atQuery = ""
}

const (
	fileScanMaxResults        = 500
	fileScanMaxDirsVisited    = 2000
	fileScanMaxDepth          = 6
	fileSuggestionViewSize    = 8
	commandSuggestionViewSize = 8
)

func (s *State) updatefileSuggestions(query string) {
	s.suggestionType = TypeFile
	s.suggestions = nil
	s.fileSuggestions = nil

	if s.cwd == "" {
		s.visible = false
		return
	}

	if s.allFilesCwd != s.cwd {
		s.allFiles = s.scanAllFiles()
		s.allFilesCwd = s.cwd
	}

	s.fileSuggestions = filterFiles(s.allFiles, query)
	s.sortSuggestions()

	s.visible = len(s.fileSuggestions) > 0
	if s.selectedIdx >= len(s.fileSuggestions) {
		s.selectedIdx = 0
	}
	s.clampViewStart()
}

var supportedFileExtensions = map[string]bool{
	".md":   true,
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
}

func (s *State) scanAllFiles() []fileSuggestion {
	seen := make(map[string]bool)
	var results []fileSuggestion

	type queueItem struct {
		dir   string
		depth int
	}
	queue := []queueItem{{s.cwd, 0}}
	dirsVisited := 0

	for len(queue) > 0 && len(results) < fileScanMaxResults && dirsVisited < fileScanMaxDirsVisited {
		item := queue[0]
		queue = queue[1:]

		if item.depth > fileScanMaxDepth {
			continue
		}

		entries, err := os.ReadDir(item.dir)
		if err != nil {
			continue
		}
		dirsVisited++

		for _, entry := range entries {
			if len(results) >= fileScanMaxResults {
				break
			}

			name := entry.Name()
			fullPath := filepath.Join(item.dir, name)

			if entry.IsDir() {
				if !shouldSkipDirectory(name) && item.depth < fileScanMaxDepth {
					queue = append(queue, queueItem{fullPath, item.depth + 1})
				}
				continue
			}

			ext := strings.ToLower(filepath.Ext(name))
			if !supportedFileExtensions[ext] {
				continue
			}

			relPath, err := filepath.Rel(s.cwd, fullPath)
			if err != nil || seen[relPath] {
				continue
			}
			seen[relPath] = true

			results = append(results, fileSuggestion{
				Path:        relPath,
				DisplayName: relPath,
				IsDir:       false,
			})
		}
	}

	return results
}

func filterFiles(all []fileSuggestion, query string) []fileSuggestion {
	if query == "" {
		out := make([]fileSuggestion, len(all))
		copy(out, all)
		return out
	}
	queryLower := strings.ToLower(query)
	var filtered []fileSuggestion
	for _, f := range all {
		if kit.FuzzyMatch(strings.ToLower(f.Path), queryLower) {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

func (s *State) sortSuggestions() {
	sort.SliceStable(s.fileSuggestions, func(i, j int) bool {
		depthI := strings.Count(s.fileSuggestions[i].Path, "/")
		depthJ := strings.Count(s.fileSuggestions[j].Path, "/")
		if depthI != depthJ {
			return depthI < depthJ
		}
		return len(s.fileSuggestions[i].Path) < len(s.fileSuggestions[j].Path)
	})
}

func shouldSkipDirectory(name string) bool {
	if strings.HasPrefix(name, ".") && name != ".san" {
		return true
	}

	switch name {
	case "node_modules", "vendor", ".git", "__pycache__", "dist", "build",
		"target", "DerivedData", "Pods", "coverage":
		return true
	}
	return false
}

func (s *State) SetCwd(cwd string) {
	if s.cwd != cwd {
		s.cwd = cwd
		s.allFiles = nil
		s.allFilesCwd = ""
	}
}

func (s *State) MoveUp() {
	if s.selectedIdx > 0 {
		s.selectedIdx--
		s.clampViewStart()
	}
}

func (s *State) MoveDown() {
	maxIdx := s.maxSelectedIdx()
	if s.selectedIdx < maxIdx {
		s.selectedIdx++
		s.clampViewStart()
	}
}

func (s *State) MovePageUp() {
	pageSize := s.suggestionViewSize()
	s.selectedIdx = max(s.selectedIdx-pageSize, 0)
	s.clampViewStart()
}

func (s *State) MovePageDown() {
	pageSize := s.suggestionViewSize()
	maxIdx := s.maxSelectedIdx()
	s.selectedIdx += pageSize
	if s.selectedIdx > maxIdx {
		s.selectedIdx = maxIdx
	}
	if s.selectedIdx < 0 {
		s.selectedIdx = 0
	}
	s.clampViewStart()
}

func (s *State) MoveToTop() {
	s.selectedIdx = 0
	s.viewStart = 0
}

func (s *State) MoveToEnd() {
	s.selectedIdx = max(s.maxSelectedIdx(), 0)
	s.clampViewStart()
}

func (s *State) maxSelectedIdx() int {
	if s.suggestionType == TypeFile {
		return len(s.fileSuggestions) - 1
	}
	return len(s.suggestions) - 1
}

func (s *State) clampViewStart() {
	viewSize := s.suggestionViewSize()
	total := s.totalSuggestions()
	if total == 0 {
		s.viewStart = 0
		return
	}
	maxStart := max(total-viewSize, 0)
	if s.viewStart > maxStart {
		s.viewStart = maxStart
	}
	if s.viewStart < 0 {
		s.viewStart = 0
	}
	if s.selectedIdx < s.viewStart {
		s.viewStart = s.selectedIdx
	} else if s.selectedIdx >= s.viewStart+viewSize {
		s.viewStart = s.selectedIdx - viewSize + 1
	}
}

func (s *State) suggestionViewSize() int {
	if s.suggestionType == TypeFile {
		return fileSuggestionViewSize
	}
	return commandSuggestionViewSize
}

func (s *State) totalSuggestions() int {
	if s.suggestionType == TypeFile {
		return len(s.fileSuggestions)
	}
	return len(s.suggestions)
}

func (s *State) GetSelected() string {
	if !s.visible {
		return ""
	}

	if s.suggestionType == TypeFile {
		if len(s.fileSuggestions) == 0 || s.selectedIdx >= len(s.fileSuggestions) {
			return ""
		}
		return s.fileSuggestions[s.selectedIdx].Path
	}

	if len(s.suggestions) == 0 || s.selectedIdx >= len(s.suggestions) {
		return ""
	}
	return "/" + s.suggestions[s.selectedIdx].Name
}

func (s *State) GetSuggestionType() Type {
	return s.suggestionType
}

func (s *State) Hide() {
	s.visible = false
}

func (s *State) IsVisible() bool {
	if s.suggestionType == TypeFile {
		return s.visible && len(s.fileSuggestions) > 0
	}
	return s.visible && len(s.suggestions) > 0
}

func (s *State) Render(width int) string {
	if !s.IsVisible() {
		return ""
	}
	if s.suggestionType == TypeFile {
		return s.renderfileSuggestions(width)
	}
	return s.renderCommandSuggestions(width)
}

func (s *State) renderfileSuggestions(width int) string {
	total := len(s.fileSuggestions)
	viewSize := min(fileSuggestionViewSize, total)

	start := s.viewStart
	if start+viewSize > total {
		start = total - viewSize
	}
	if start < 0 {
		start = 0
	}
	end := min(start+viewSize, total)
	items := s.fileSuggestions[start:end]

	boxWidth := clampInt(width*60/100, 40, 60)

	var lines []string
	headerStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Primary).Bold(true)
	header := "@ Import file:"
	if total > viewSize {
		header = fmt.Sprintf("@ Import file (%d/%d):", s.selectedIdx+1, total)
	}
	lines = append(lines, headerStyle.Render(header))

	maxPathLen := boxWidth - 10
	for i, file := range items {
		icon := "📄"
		if file.IsDir {
			icon = "📁"
		}

		displayPath := truncateFromLeft(file.DisplayName, maxPathLen)
		line := fmt.Sprintf("%s %s", icon, displayPath)

		if start+i == s.selectedIdx {
			lines = append(lines, selectedSuggestionStyle().Render("> "+line))
		} else {
			lines = append(lines, normalSuggestionStyle().Render("  "+line))
		}
	}

	hint := "Tab/Enter to select · Esc to cancel"
	if total > viewSize {
		hint = "↑/↓ scroll · Tab/Enter · Esc"
	}
	lines = append(lines, "", commandDescStyle().Render(hint))

	content := strings.Join(lines, "\n")
	return suggestionBoxStyle().Width(boxWidth).Render(content)
}

func (s *State) renderCommandSuggestions(width int) string {
	total := len(s.suggestions)
	viewSize := min(commandSuggestionViewSize, total)

	start := s.viewStart
	if start+viewSize > total {
		start = total - viewSize
	}
	if start < 0 {
		start = 0
	}
	end := min(start+viewSize, total)
	items := s.suggestions[start:end]

	boxWidth := max(width-2, 40)
	contentWidth := max(boxWidth-2, 20)

	var lines []string
	headerStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Primary).Bold(true)
	header := "Commands:"
	if total > viewSize {
		header = fmt.Sprintf("Commands (%d/%d):", s.selectedIdx+1, total)
	}
	lines = append(lines, headerStyle.Render(header))

	for i, cmd := range items {
		cmdName := "/" + cmd.Name
		maxDescLen := max(contentWidth-len(cmdName)-3, 10)
		desc := truncateWithEllipsis(cmd.Description, maxDescLen)

		if start+i == s.selectedIdx {
			line := fmt.Sprintf("%s - %s", cmdName, desc)
			lines = append(lines, selectedSuggestionStyle().Render(line))
		} else {
			lines = append(lines, commandNameStyle().Render(cmdName)+commandDescStyle().Render(" - "+desc))
		}
	}

	hint := "Tab/Enter to select · Esc to cancel"
	if total > viewSize {
		hint = "↑/↓ scroll · Tab/Enter · Esc"
	}
	lines = append(lines, "", commandDescStyle().Render(hint))

	content := strings.Join(lines, "\n")
	return suggestionBoxStyle().Width(boxWidth).Render(content)
}

func clampInt(value, minVal, maxVal int) int {
	return max(minVal, min(value, maxVal))
}

func truncateWithEllipsis(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func truncateFromLeft(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[len(runes)-maxLen:])
	}
	return "..." + string(runes[len(runes)-maxLen+3:])
}
