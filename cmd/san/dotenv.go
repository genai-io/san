package main

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"

	"github.com/genai-io/san/internal/setting"
)

// loadWorkingDirEnv gives the launch directory's .env precedence once eligible.
func loadWorkingDirEnv() {
	cwd, err := os.Getwd()
	if err != nil || setting.WorkingDirEnvNeedsTrust(cwd) {
		return
	}
	_ = godotenv.Overload(filepath.Join(cwd, ".env"))
}
