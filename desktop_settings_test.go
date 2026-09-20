//go:build desktop

package main

import "testing"

func TestDesktopProjectsSurviveSettingsUpdates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	m := &desktopManager{sessions: map[uint]*desktopSession{1: {root: root}}}

	if err := updateSettingsMap(map[string]any{"editor.fontSize": 16.0}); err != nil {
		t.Fatal(err)
	}
	m.touchRecent(root)
	m.saveOpenProjects()
	if err := writeSettings(settings{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := updateSettingsMap(map[string]any{"editor.vimMode": true}); err != nil {
		t.Fatal(err)
	}

	saved := readSettings()
	if len(saved.RecentProjects) != 1 || saved.RecentProjects[0].Path != root {
		t.Fatalf("recent projects were lost: %+v", saved.RecentProjects)
	}
	if len(saved.OpenProjects) != 1 || saved.OpenProjects[0] != root {
		t.Fatalf("open projects were lost: %v", saved.OpenProjects)
	}

	m.removeRecent(root)
	saved = readSettings()
	if len(saved.RecentProjects) != 0 || len(saved.OpenProjects) != 0 {
		t.Fatalf("removed project was retained: %+v", saved)
	}
	if saved.Agent != "claude" || saved.EditorFontSize == nil || *saved.EditorFontSize != 16 || saved.EditorVimMode == nil || !*saved.EditorVimMode {
		t.Fatalf("desktop updates overwrote user preferences: %+v", saved)
	}

	m.sessions = nil
	m.saveOpenProjects()
	if len(readSettings().OpenProjects) != 0 {
		t.Fatal("closing all projects left stale restoration state")
	}
}

func TestDesktopSessionCloseStopsGitWatcher(t *testing.T) {
	watcher := NewGitWatcher(NewIndex(t.TempDir()))
	session := &desktopSession{server: &Server{gitWatcher: watcher}}
	session.close()
	session.close()
	select {
	case <-watcher.stopCh:
	default:
		t.Fatal("closing a desktop session left its git watcher running")
	}
}
