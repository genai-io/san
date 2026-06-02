package kit

// BrailleSpinnerFrames is the canonical 10-frame braille spinner for
// in-flight indicators on Unicode-capable terminals (e.g. the provider
// connect/refresh selector). Sharing a single table avoids drift the next
// time a non-Unicode TTY fallback or denser glyph set lands.
var BrailleSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
