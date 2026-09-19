package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/genai-io/san/internal/autoupdate"
)

func runUpdate(ctx context.Context) error {
	current := strings.TrimPrefix(version, "v")
	fmt.Printf("Current version: v%s\n", current)

	latest, err := autoupdate.Latest(ctx)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}
	fmt.Printf("Latest version:  v%s\n", latest)

	// Equality, not Newer: an explicit `san update` from a dev build or a
	// newer local release is the user's call — both versions are on screen
	// and the confirm below asks.
	if latest == current {
		fmt.Println("Already up to date.")
		return nil
	}
	fmt.Printf("New version available: v%s -> v%s\n", current, latest)
	if !confirm("Download and install?") {
		fmt.Println("Update cancelled.")
		return nil
	}

	fmt.Printf("Downloading v%s ...\n", latest)
	bar := &progressBar{}
	err = autoupdate.Install(ctx, latest, bar.report)
	bar.clear()
	if err != nil {
		return err
	}
	fmt.Printf("Updated to v%s\n", latest)
	fmt.Println("Restart san to use the new version.")
	return nil
}

// progressBar redraws a download bar on stderr whenever the percentage moves.
type progressBar struct {
	lastPct int
}

func (p *progressBar) report(written, total int64) {
	if total <= 0 {
		return
	}
	pct := int(written * 100 / total)
	if pct == p.lastPct {
		return
	}
	p.lastPct = pct
	const width = 30
	filled := min(pct*width/100, width)
	fmt.Fprintf(os.Stderr, "\r  downloading [%s%s] %3d%%",
		strings.Repeat("█", filled), strings.Repeat("░", width-filled), pct)
}

func (p *progressBar) clear() {
	if p.lastPct > 0 {
		fmt.Fprint(os.Stderr, "\r"+strings.Repeat(" ", 60)+"\r")
	}
}

// confirm prompts the user for a yes/no answer and returns true for "yes".
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}
