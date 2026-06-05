package kit

// SaveLevel represents where to save settings (project vs user scope).
type SaveLevel int

const (
	SaveLevelProject SaveLevel = iota // Save to .san/<feature>.json
	SaveLevelUser                     // Save to ~/.san/<feature>.json
)

// String returns the display name for the save level.
func (l SaveLevel) String() string {
	if l == SaveLevelUser {
		return "User"
	}
	return "Project"
}
