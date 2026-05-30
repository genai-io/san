package kit

// BrailleSpinnerFrames is the canonical 10-frame braille spinner used by
// every in-flight indicator across the TUI (provider connect/refresh,
// selflearn review, …). Sharing a single table avoids drift the next time
// a non-Unicode TTY fallback or denser glyph set lands.
var BrailleSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
