package kit

import "time"

// StarSpinnerFrames is the 4pt → 6pt → 8pt → 6pt rotating-sparkle spinner shown
// while the model is thinking/streaming and in the /autopilot mission dialog.
// Sharing one table keeps the app's most visible animation identical across
// those live views. Pair it with StarSpinnerFPS.
var StarSpinnerFrames = []string{"✦", "✶", "✸", "✶"}

// StarSpinnerFPS is the per-frame interval for StarSpinnerFrames.
const StarSpinnerFPS = 360 * time.Millisecond

// BrailleSpinnerFrames is the canonical 10-frame braille spinner for
// in-flight indicators on Unicode-capable terminals (e.g. the provider
// connect/refresh selector). Sharing a single table avoids drift the next
// time a non-Unicode TTY fallback or denser glyph set lands.
var BrailleSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// AsciiSpinnerFrames is the classic four-frame ASCII spinner used by
// surfaces that need to stay terminal-portable — some PTYs render braille
// as wide cells, which jitters the width of the surrounding label.
var AsciiSpinnerFrames = []string{"|", "/", "-", `\`}
