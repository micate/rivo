package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// rivo keeps no state inside a workspace. The one choice worth remembering
// between runs is which coding harness may edit it, so it is stored with the
// other per-user files rivo already writes (~/.rivo), never in the working tree.

type recentProject struct {
	Path       string `json:"path"`
	LastOpened int64  `json:"lastOpened"`
}

type settings struct {
	Agent          string            `json:"agent,omitempty"`
	RecentProjects []recentProject   `json:"recentProjects,omitempty"`
	OpenProjects   []string          `json:"openProjects,omitempty"`
	Models         map[string]string `json:"models,omitempty"`
}

var settingsMu sync.Mutex

// settingsPath mirrors stateFilePath in update.go: honour the XDG location when
// it is set, otherwise fall back to ~/.rivo.
func settingsPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "rivo", "settings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".rivo", "settings.json")
}

// readSettings never fails: a missing or corrupt file simply means no choice
// has been made yet, which is the same as a fresh install.
func readSettings() settings {
	var s settings
	p := settingsPath()
	if p == "" {
		return s
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	data, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	return s
}

func writeSettings(s settings) error {
	p := settingsPath()
	if p == "" {
		return errors.New("no home directory to save settings in")
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}
