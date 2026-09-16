package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunSelfUpdateMock(t *testing.T) {
	// Create a dummy binary that will act as the "updated" binary
	tmpDir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	newBinPath := filepath.Join(tmpDir, "new-rivo"+ext)
	// Write a shell script / batch script or copy existing current test binary
	script := "#!/bin/sh\necho 'rivo 0.2.0 (" + runtime.GOOS + "/" + runtime.GOARCH + ")'\n"
	if err := os.WriteFile(newBinPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	assetName := fmt.Sprintf("rivo-0.2.0-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)

	// Setup mock server
	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/rivo/releases/latest":
			rel := githubRelease{
				TagName: "v0.2.0",
				Name:    "rivo v0.2.0",
				Assets: []struct {
					Name               string `json:"name"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{
					{
						Name:               assetName,
						BrowserDownloadURL: serverURL + "/download/" + assetName,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rel)
		case "/download/" + assetName:
			http.ServeFile(w, r, newBinPath)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	serverURL = ts.URL

	t.Setenv("RIVO_REPO", "test/rivo")
	t.Setenv("RIVO_UPDATE_URL", ts.URL+"/repos/test/rivo/releases/latest")
	t.Setenv("XDG_STATE_HOME", tmpDir)

	rel, err := fetchLatestRelease("test/rivo")
	if err != nil {
		t.Fatalf("fetchLatestRelease returned error: %v", err)
	}
	if rel.TagName != "v0.2.0" {
		t.Fatalf("expected v0.2.0, got %s", rel.TagName)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != assetName {
		t.Fatalf("expected asset %s, got %+v", assetName, rel.Assets)
	}
}
