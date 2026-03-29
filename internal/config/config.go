package config

import (
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// Config holds generic browser automation settings.
type Config struct {
	Headed     string // "1" = always headed, "auto" = per-action, "" = headless
	Debug      bool   // verbose logging + failure screenshots
	ProfileDir string // chrome profile path
}

// GlobalEnvPath is the stable config location that survives branch switches.
func GlobalEnvPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "brz", "agent.env")
}

// Load reads config from ~/.config/brz/agent.env first (stable),
// then .env in working directory (override for testing).
func Load() *Config {
	// Load global env first
	globalPath := GlobalEnvPath()
	if _, err := os.Stat(globalPath); err == nil {
		_ = godotenv.Load(globalPath)
	}
	// Load local .env (only sets keys not already present)
	_ = godotenv.Load()

	home, _ := os.UserHomeDir()

	cfg := &Config{
		Headed:     os.Getenv("BRZ_HEADED"), // "1", "auto", or ""
		Debug:      os.Getenv("BRZ_DEBUG") == "1",
		ProfileDir: filepath.Join(home, ".config", "brz", "chrome-profile"),
	}

	// Allow override of profile dir
	if dir := os.Getenv("BRZ_PROFILE_DIR"); dir != "" {
		cfg.ProfileDir = dir
	}

	return cfg
}
