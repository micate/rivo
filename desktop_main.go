//go:build desktop

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed web/logo/rivo-app-icon.png
var desktopIcon []byte

type desktopSession struct {
	root      string
	server    *Server
	lsp       *lspManager
	agent     *agentManager
	closeOnce sync.Once
}

func newDesktopSession(root string) (*desktopSession, error) {
	root, err := canonicalProject(root)
	if err != nil {
		return nil, err
	}
	ix := NewIndex(root)
	lsp := newLSPManager(root, true)
	agent, err := newAgentManager(root, "", lsp)
	if err != nil {
		lsp.Close()
		return nil, err
	}
	srv := NewServer(ix, lsp)
	srv.SetAgent(agent)
	go ix.Build()
	return &desktopSession{root: root, server: srv, lsp: lsp, agent: agent}, nil
}

func (s *desktopSession) close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		// Close synchronously: WindowClosing may be the final event before the
		// application process exits, so cleanup must not be left in a goroutine.
		if s.server != nil && s.server.gitWatcher != nil {
			s.server.gitWatcher.Stop()
		}
		if s.agent != nil {
			s.agent.Close()
		}
		if s.lsp != nil {
			s.lsp.Close()
		}
	})
}

type desktopManager struct {
	mu       sync.RWMutex
	app      *application.App
	windows  map[uint]application.Window
	sessions map[uint]*desktopSession
	quitting bool
}

type desktopRecent struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	LastOpened int64  `json:"lastOpened"`
	Exists     bool   `json:"exists"`
}

func canonicalProject(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty project path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("project is unavailable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		resolved = filepath.Dir(resolved)
	}
	return resolved, nil
}

func (m *desktopManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/desktop/") {
		m.serveDesktopAPI(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if session := m.sessionForRequest(r); session != nil {
			session.server.ServeHTTP(w, r)
			return
		}
		fail(w, http.StatusConflict, "no project is open in this window")
		return
	}
	serveDesktopAsset(w, r)
}

func (m *desktopManager) requestWindow(r *http.Request) application.Window {
	id, _ := strconv.ParseUint(r.Header.Get("x-wails-window-id"), 10, 64)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.windows[uint(id)]
}

func (m *desktopManager) sessionForRequest(r *http.Request) *desktopSession {
	id, _ := strconv.ParseUint(r.Header.Get("x-wails-window-id"), 10, 64)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[uint(id)]
}

func (m *desktopManager) serveDesktopAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/desktop/state":
		window := m.requestWindow(r)
		if window == nil {
			http.NotFound(w, r)
			return
		}
		session := m.sessionForRequest(r)
		m.mu.RLock()
		singleWindow := len(m.windows) == 1
		m.mu.RUnlock()
		writeJSON(w, map[string]any{
			"desktop":      true,
			"mode":         map[bool]string{true: "project", false: "welcome"}[session != nil],
			"recents":      m.recents(),
			"singleWindow": singleWindow,
		})
	case "/desktop/open":
		if r.Method != http.MethodPost {
			fail(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		window := m.requestWindow(r)
		if window == nil {
			fail(w, http.StatusBadRequest, "unknown window")
			return
		}
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			var err error
			path, err = m.chooseProject(window)
			if err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			if path == "" {
				writeJSON(w, map[string]any{"cancelled": true})
				return
			}
		}
		reused, err := m.openProject(window, path)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"opened": true, "reload": reused})
	case "/desktop/new-window":
		if r.Method != http.MethodPost {
			fail(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		m.newWindow(nil)
		writeJSON(w, map[string]any{"opened": true})
	case "/desktop/remove":
		if r.Method != http.MethodPost {
			fail(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		m.removeRecent(r.URL.Query().Get("path"))
		writeJSON(w, map[string]any{"removed": true, "recents": m.recents()})
	default:
		http.NotFound(w, r)
	}
}

func (m *desktopManager) chooseProject(window application.Window) (string, error) {
	return m.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("Open Project").
		SetButtonText("Open").
		AttachToWindow(window).
		PromptForSingleSelection()
}

// openProject reuses a welcome window. From an existing project it creates a
// second window, which macOS groups into the native tab bar.
func (m *desktopManager) openProject(source application.Window, path string) (bool, error) {
	session, err := newDesktopSession(path)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	_, alreadyProject := m.sessions[source.ID()]
	m.mu.Unlock()
	if alreadyProject {
		m.newWindow(session)
		return false, nil
	}

	m.mu.Lock()
	m.sessions[source.ID()] = session
	m.mu.Unlock()
	source.SetTitle(filepath.Base(session.root) + " — Rivo")
	m.touchRecent(session.root)
	m.saveOpenProjects()
	return true, nil
}

func (m *desktopManager) newWindow(session *desktopSession) application.Window {
	title := "Rivo"
	if session != nil {
		title = filepath.Base(session.root) + " — Rivo"
	}
	m.mu.RLock()
	firstWindow := len(m.windows) == 0
	m.mu.RUnlock()
	titleBar := application.MacTitleBarDefault
	if firstWindow {
		titleBar = application.MacTitleBarHidden
	}
	window := m.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     title,
		Width:     1280,
		Height:    820,
		MinWidth:  760,
		MinHeight: 520,
		URL:       "/",
		Mac: application.MacWindow{
			TabbingMode: application.MacWindowTabbingModePreferred,
			TitleBar:    titleBar,
		},
		UseApplicationMenu: true,
		KeyBindings: map[string]func(application.Window){
			"CmdOrCtrl+O": func(w application.Window) { go m.openFromShortcut(w) },
			"CmdOrCtrl+N": func(application.Window) { m.newWindow(nil) },
			"CmdOrCtrl+W": func(w application.Window) { w.Close() },
		},
	})

	m.mu.Lock()
	m.windows[window.ID()] = window
	if session != nil {
		m.sessions[window.ID()] = session
	}
	windowCount := len(m.windows)
	m.mu.Unlock()
	m.setSingleWindowMode(windowCount == 1)
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		m.windowClosed(window.ID())
	})
	if session != nil {
		m.touchRecent(session.root)
		m.saveOpenProjects()
	}
	return window
}

func (m *desktopManager) openFromShortcut(window application.Window) {
	path, err := m.chooseProject(window)
	if err != nil || path == "" {
		return
	}
	reused, err := m.openProject(window, path)
	if err == nil && reused {
		window.Reload()
	}
}

func (m *desktopManager) windowClosed(id uint) {
	m.mu.Lock()
	session := m.sessions[id]
	delete(m.sessions, id)
	delete(m.windows, id)
	quitting := m.quitting
	windowCount := len(m.windows)
	empty := windowCount == 0
	m.mu.Unlock()
	session.close()
	if !quitting {
		m.saveOpenProjects()
		if empty {
			m.newWindow(nil)
		} else {
			m.setSingleWindowMode(windowCount == 1)
		}
	}
}

func (m *desktopManager) setSingleWindowMode(single bool) {
	setNativeSingleWindowTitleBar(single)
	js := fmt.Sprintf("document.body.classList.toggle('native-single-window', %t)", single)
	m.mu.RLock()
	windows := make([]application.Window, 0, len(m.windows))
	for _, window := range m.windows {
		windows = append(windows, window)
	}
	m.mu.RUnlock()
	for _, window := range windows {
		window.ExecJS(js)
	}
}

func (m *desktopManager) recents() []desktopRecent {
	s := readSettings()
	out := make([]desktopRecent, 0, len(s.RecentProjects))
	for _, item := range s.RecentProjects {
		info, err := os.Stat(item.Path)
		out = append(out, desktopRecent{
			Path: item.Path, Name: filepath.Base(item.Path), LastOpened: item.LastOpened,
			Exists: err == nil && info.IsDir(),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastOpened > out[j].LastOpened })
	return out
}

func (m *desktopManager) touchRecent(path string) {
	s := readSettings()
	recent := []recentProject{{Path: path, LastOpened: time.Now().Unix()}}
	for _, item := range s.RecentProjects {
		if item.Path != path {
			recent = append(recent, item)
		}
		if len(recent) == 12 {
			break
		}
	}
	_ = updateSettingsMap(map[string]any{"recentProjects": recent})
}

func (m *desktopManager) removeRecent(path string) {
	s := readSettings()
	filtered := s.RecentProjects[:0]
	for _, item := range s.RecentProjects {
		if item.Path != path {
			filtered = append(filtered, item)
		}
	}
	open := s.OpenProjects[:0]
	for _, item := range s.OpenProjects {
		if item != path {
			open = append(open, item)
		}
	}
	_ = updateSettingsMap(map[string]any{
		"recentProjects": filtered,
		"openProjects":   open,
	})
}

func (m *desktopManager) openProjectPaths() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	paths := make([]string, 0, len(m.sessions))
	seen := make(map[string]bool)
	for _, session := range m.sessions {
		if session != nil && !seen[session.root] {
			seen[session.root] = true
			paths = append(paths, session.root)
		}
	}
	sort.Strings(paths)
	return paths
}

func (m *desktopManager) saveOpenProjects() {
	_ = updateSettingsMap(map[string]any{"openProjects": m.openProjectPaths()})
}

func (m *desktopManager) beginQuit() {
	m.mu.Lock()
	m.quitting = true
	m.mu.Unlock()
	m.saveOpenProjects()
}

func (m *desktopManager) shutdown() {
	m.mu.Lock()
	sessions := make([]*desktopSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[uint]*desktopSession)
	m.mu.Unlock()
	for _, session := range sessions {
		session.close()
	}
}

func serveDesktopAsset(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	if path == "/static/themes.css" {
		names, _ := fs.Glob(assets, "web/themes/*.css")
		var css strings.Builder
		for _, name := range names {
			data, err := fs.ReadFile(assets, name)
			if err == nil {
				css.Write(data)
				css.WriteByte('\n')
			}
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(css.String()))
		return
	}
	// The browser server exposes files from web/ under /static/. Mirror that
	// mapping in Wails rather than looking for a non-existent web/static/ dir.
	name := "web" + path
	if strings.HasPrefix(path, "/static/") {
		name = "web/" + strings.TrimPrefix(path, "/static/")
	}
	data, err := fs.ReadFile(assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (m *desktopManager) installMenu() {
	menu := m.app.NewMenu()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	file := menu.AddSubmenu("File")
	file.Add("New Window").SetAccelerator("CmdOrCtrl+N").OnClick(func(*application.Context) { m.newWindow(nil) })
	file.Add("Open Project…").SetAccelerator("CmdOrCtrl+O").OnClick(func(*application.Context) {
		if window := m.app.Window.Current(); window != nil {
			go m.openFromShortcut(window)
		}
	})
	file.AddSeparator()
	file.AddRole(application.CloseWindow)
	if runtime.GOOS != "darwin" {
		file.AddRole(application.Quit)
	}
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.ViewMenu)
	menu.AddRole(application.WindowMenu)
	m.app.Menu.Set(menu)
}

func main() {
	manager := &desktopManager{
		windows:  make(map[uint]application.Window),
		sessions: make(map[uint]*desktopSession),
	}
	app := application.New(application.Options{
		Name:        "Rivo",
		Description: "Fast, remote-first code navigator",
		Icon:        desktopIcon,
		Assets:      application.AssetOptions{Handler: manager},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Windows:     application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:       application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "Rivo"},
		ShouldQuit:  func() bool { manager.beginQuit(); return true },
		OnShutdown:  manager.shutdown,
	})
	manager.app = app
	manager.installMenu()

	paths := os.Args[1:]
	if len(paths) == 0 {
		paths = readSettings().OpenProjects
	}
	opened := 0
	for _, path := range paths {
		session, err := newDesktopSession(path)
		if err != nil {
			continue
		}
		manager.newWindow(session)
		opened++
	}
	if opened == 0 {
		manager.newWindow(nil)
	}
	// Drop projects that disappeared since the previous run from the restore
	// set. They remain in Recents as "Missing" until the user removes them.
	manager.saveOpenProjects()

	// Clicking the Dock icon after all windows were closed should always bring
	// back the start screen.
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidBecomeActive, func(*application.ApplicationEvent) {
		manager.mu.RLock()
		empty := len(manager.windows) == 0
		manager.mu.RUnlock()
		if empty {
			manager.newWindow(nil)
		}
	})

	if err := app.Run(); err != nil {
		data, _ := json.Marshal(err.Error())
		log.Fatalf("Rivo desktop failed: %s", data)
	}
}
