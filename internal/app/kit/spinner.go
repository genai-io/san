package kit

// BrailleSpinnerFrames is the canonical 10-frame braille spinner for
// in-flight indicators on Unicode-capable terminals (e.g. the provider
// connect/refresh selector). Sharing a single table avoids drift the next
// time a non-Unicode TTY fallback or denser glyph set lands.
var BrailleSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// AsciiSpinnerFrames is the classic four-frame ASCII spinner used by
// surfaces that need to stay terminal-portable — some PTYs render braille
// as wide cells, which jitters the width of the surrounding label.
var AsciiSpinnerFrames = []string{"|", "/", "-", `\`}
