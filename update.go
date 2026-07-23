package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubOwner       = "javimosch"
	githubRepo        = "remotecmd-cli"
	updateNudgeFile   = "nudge-check"
	updateNudgeFreq    = 1 * time.Hour
	updateDownloadTO   = 120 * time.Second
	updateVersionTO    = 10 * time.Second
	updateNudgeTO      = 3 * time.Second
	exitUpdateAvail    = 5  // --check: update available (not an error)
	exitUpdateFail     = 100 // download/verify/smoke/swap failure
)

// githubRelease represents the relevant fields from GitHub's releases/latest API.
type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// latestReleaseInfo fetches the latest release from GitHub API.
func latestReleaseInfo() (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)
	client := &http.Client{Timeout: updateVersionTO}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github API returned %d", resp.StatusCode)
	}
	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// assetNameForPlatform returns the expected asset name for the current OS/arch.
func assetNameForPlatform() string {
	return fmt.Sprintf("remotecmd-cli-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// findAsset returns the download URL for the current platform's binary.
func (rel *githubRelease) findAsset() (string, error) {
	want := assetNameForPlatform()
	for _, a := range rel.Assets {
		if a.Name == want {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no binary for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, rel.TagName)
}

// findChecksumsURL returns the URL for checksums.txt if present.
func (rel *githubRelease) findChecksumsURL() string {
	for _, a := range rel.Assets {
		if a.Name == "checksums.txt" {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// fileSHA256 computes the full sha256 hex of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// downloadFile downloads url to dest with a timeout.
func downloadFile(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTO)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// fetchChecksums downloads and parses checksums.txt, returns map[filename]sha256.
func fetchChecksums(url string) map[string]string {
	resp, err := http.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	m := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			m[parts[1]] = parts[0]
		}
	}
	return m
}

// smokeTestBinary runs `<path> version` to verify the binary is not corrupt.
func smokeTestBinary(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("smoke test failed: %w", err)
	}
	if !strings.Contains(string(out), "remotecmd-cli version") {
		return fmt.Errorf("smoke test: unexpected output: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// handleUpdate implements `remotecmd-cli update [--check] [--force]`.
func handleUpdate(args []string) {
	var checkOnly, force bool
	for _, a := range args {
		switch a {
		case "--check":
			checkOnly = true
		case "--force":
			force = true
		default:
			fmt.Fprintf(os.Stderr, "Usage: remotecmd-cli update [--check] [--force]\n")
			osExit(ExitConfigError)
		}
	}

	// Fetch latest release info from GitHub
	rel, err := latestReleaseInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[update] cannot reach GitHub: %v\n", err)
		osExit(exitUpdateFail)
	}

	latestTag := strings.TrimPrefix(rel.TagName, "v")
	currentTag := Version

	if latestTag == currentTag && !force {
		fmt.Printf(`{"ok":true,"version":"%s","up_to_date":true}`+"\n", currentTag)
		return
	}

	if checkOnly {
		fmt.Printf(`{"ok":true,"local":"%s","remote":"%s","up_to_date":false}`+"\n", currentTag, latestTag)
		osExit(exitUpdateAvail)
	}

	// Find the binary for this platform
	dlURL, err := rel.findAsset()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[update] %v\n", err)
		osExit(exitUpdateFail)
	}

	fmt.Fprintf(os.Stderr, "[update] %s → %s; downloading…\n", currentTag, latestTag)

	// Get executable path
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[update] cannot determine executable path: %v\n", err)
		osExit(exitUpdateFail)
	}
	exe, _ = resolveSymlink(exe)

	// Download to temp file in same directory (for atomic rename)
	tmp := fmt.Sprintf("%s.new.%d", exe, os.Getpid())
	if err := downloadFile(dlURL, tmp); err != nil {
		fmt.Fprintf(os.Stderr, "[update] download failed: %v\n", err)
		osExit(exitUpdateFail)
	}

	// Verify hash against checksums.txt
	checksumsURL := rel.findChecksumsURL()
	if checksumsURL != "" {
		checksums := fetchChecksums(checksumsURL)
		wantHash, ok := checksums[assetNameForPlatform()]
		if ok {
			gotHash, err := fileSHA256(tmp)
			if err != nil {
				os.Remove(tmp)
				fmt.Fprintf(os.Stderr, "[update] cannot hash download: %v\n", err)
				osExit(exitUpdateFail)
			}
			if gotHash != wantHash {
				os.Remove(tmp)
				fmt.Fprintf(os.Stderr, "[update] hash mismatch (%s != %s)\n", gotHash[:12], wantHash[:12])
				osExit(exitUpdateFail)
			}
		}
	}

	// Smoke test: the new binary must run `version`
	os.Chmod(tmp, 0o755)
	if err := smokeTestBinary(tmp); err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "[update] %v\n", err)
		osExit(exitUpdateFail)
	}

	// Atomic swap: current → .bak, new → in place
	bak := exe + ".bak"
	os.Remove(bak) // remove old .bak if exists
	if err := os.Rename(exe, bak); err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "[update] cannot move current to .bak: %v\n", err)
		osExit(exitUpdateFail)
	}
	if err := os.Rename(tmp, exe); err != nil {
		// Rollback
		os.Rename(bak, exe)
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "[update] swap failed; rolled back: %v\n", err)
		osExit(exitUpdateFail)
	}
	os.Chmod(exe, 0o755)

	fmt.Fprintf(os.Stderr, "[update] updated %s → %s (backup: %s)\n", currentTag, latestTag, bak)
	fmt.Printf(`{"ok":true,"updated":true,"from":"%s","to":"%s","backup":"%s"}`+"\n", currentTag, latestTag, bak)
}

// resolveSymlink follows symlinks to get the real path.
func resolveSymlink(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path, nil
	}
	return resolved, nil
}

// fileModTime returns the modification time of a file, or zero time if not found.
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// --- Passive nudge ---

// maybeNudge checks for a newer version on GitHub and prints a stderr nudge.
// Throttled to once per hour. Best-effort, never blocks or fails.
func maybeNudge() {
	if os.Getenv("RCMD_NO_NUDGE") == "1" {
		return
	}

	nudgePath := filepath.Join(configDir(), updateNudgeFile)
	lastCheck := fileModTime(nudgePath)
	if !lastCheck.IsZero() && time.Since(lastCheck) < updateNudgeFreq {
		return
	}

	// Touch the nudge file
	ensureConfigDir()
	os.WriteFile(nudgePath, []byte(fmt.Sprintf("%d", time.Now().Unix())), 0o644)

	// Fetch latest version (best-effort, 3s timeout)
	go func() {
		rel, err := latestReleaseInfo()
		if err != nil {
			return
		}
		latestTag := strings.TrimPrefix(rel.TagName, "v")
		if latestTag != Version && latestTag != "" {
			fmt.Fprintf(os.Stderr, "[update] a newer remotecmd-cli is available (%s → %s). Run: remotecmd-cli update\n", Version, latestTag)
		}
	}()
}
