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
// It tries `gh api` first (authenticated, 5000 req/hr) and falls back
// to unauthenticated HTTP (60 req/hr) if gh is not available or fails.
func latestReleaseInfo() (*githubRelease, error) {
	if rel, err := latestReleaseViaGH(); err == nil {
		return rel, nil
	}
	return latestReleaseViaHTTP()
}

// latestReleaseViaGH uses the gh CLI (authenticated) to fetch the release.
func latestReleaseViaGH() (*githubRelease, error) {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return nil, fmt.Errorf("gh not found: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateVersionTO)
	defer cancel()
	cmd := exec.CommandContext(ctx, ghPath, "api",
		fmt.Sprintf("repos/%s/%s/releases/latest", githubOwner, githubRepo),
		"--jq", ".")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api failed: %v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	var rel githubRelease
	if err := json.Unmarshal(out, &rel); err != nil {
		return nil, fmt.Errorf("gh api decode: %v", err)
	}
	return &rel, nil
}

// latestReleaseViaHTTP fetches the release via unauthenticated GitHub API.
func latestReleaseViaHTTP() (*githubRelease, error) {
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
	return assetCandidates(runtime.GOOS, runtime.GOARCH)[0]
}

// assetCandidates lists the release asset names that fit a platform, best
// first. Linux prefers the static build: it runs on musl (Alpine, e.g.
// supergato) as well as glibc, where the default build fails the smoke
// test. The plain name stays as a fallback for releases without -static.
// Windows assets carry .exe.
func assetCandidates(goos, goarch string) []string {
	base := fmt.Sprintf("remotecmd-cli-%s-%s", goos, goarch)
	switch goos {
	case "linux":
		return []string{base + "-static", base}
	case "windows":
		return []string{base + ".exe"}
	}
	return []string{base}
}

// findAsset returns the download URL for the current platform's binary.
// It returns the asset's name too: the checksum must be looked up under
// the asset actually downloaded.
func (rel *githubRelease) findAsset() (url, name string, err error) {
	for _, want := range assetCandidates(runtime.GOOS, runtime.GOARCH) {
		for _, a := range rel.Assets {
			if a.Name == want {
				return a.BrowserDownloadURL, a.Name, nil
			}
		}
	}
	return "", "", fmt.Errorf("no binary for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, rel.TagName)
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
func fetchChecksums(url string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateVersionTO)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("checksums.txt returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	m := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			// sha256sum -b writes "<hash> *<name>"
			m[strings.TrimPrefix(parts[1], "*")] = strings.ToLower(parts[0])
		}
	}
	return m, nil
}

// verifyReleaseChecksum checks the downloaded binary against the release's
// checksums.txt. It fails closed: a missing checksums asset, an unreachable
// or unparseable file, or no entry for this platform all refuse the update.
// Installing an unverified binary on a remote-exec tool is never the safe
// default — a truncated or swapped artifact would run on every node.
func verifyReleaseChecksum(rel *githubRelease, path, asset string) error {
	url := rel.findChecksumsURL()
	if url == "" {
		return fmt.Errorf("release %s has no checksums.txt — refusing to install an unverified binary", rel.TagName)
	}
	checksums, err := fetchChecksums(url)
	if err != nil {
		return fmt.Errorf("cannot fetch checksums.txt: %w", err)
	}
	want, ok := checksums[asset]
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %s", asset)
	}
	if len(want) != sha256.Size*2 {
		return fmt.Errorf("checksums.txt entry for %s is not a sha256 digest", asset)
	}
	got, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("cannot hash download: %w", err)
	}
	if got != want {
		return fmt.Errorf("hash mismatch (%s != %s)", got[:12], want[:12])
	}
	return nil
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
			fail(ExitConfigError, "invalid_arguments", "unknown update flag: "+a, "remotecmd-cli update [--check] [--force]")
		}
	}

	// Fetch latest release info from GitHub
	rel, err := latestReleaseInfo()
	if err != nil {
		failErrCode(exitUpdateFail, fmt.Errorf("cannot reach GitHub: %w", err))
	}

	latestTag := strings.TrimPrefix(rel.TagName, "v")
	currentTag := Version

	// Only move forward: a dev build ahead of the latest release must not
	// "update" itself back to that older release unless --force.
	if !versionLess(currentTag, latestTag) && !force {
		fmt.Printf(`{"ok":true,"version":"%s","latest":"%s","up_to_date":true}`+"\n", currentTag, latestTag)
		return
	}

	if checkOnly {
		fmt.Printf(`{"ok":true,"local":"%s","remote":"%s","up_to_date":false}`+"\n", currentTag, latestTag)
		osExit(exitUpdateAvail)
	}

	fmt.Fprintf(os.Stderr, "[update] %s → %s; downloading…\n", currentTag, latestTag)

	exe, err := os.Executable()
	if err != nil {
		failErrCode(exitUpdateFail, fmt.Errorf("cannot determine executable path: %w", err))
	}
	exe, _ = resolveSymlink(exe)

	bak, err := installRelease(rel, exe)
	if err != nil {
		failErrCode(exitUpdateFail, err)
	}

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
		if latestTag != "" && versionLess(Version, latestTag) {
			fmt.Fprintf(os.Stderr, "[update] a newer remotecmd-cli is available (%s → %s). Run: remotecmd-cli update\n", Version, latestTag)
		}
	}()
}

// installRelease downloads rel's binary for this platform, verifies its
// sha256 against the release's checksums.txt (fail closed), smoke-tests it,
// and atomically swaps it in at exe, keeping a copy of the previous one as
// exe.bak. The backup is a copy, never a move (cli-update-spec §6): moving
// the running file would make the running process's /proc/self/exe follow
// it to .bak, so an in-place restart would re-exec the old binary.
func installRelease(rel *githubRelease, exe string) (bak string, err error) {
	dlURL, asset, err := rel.findAsset()
	if err != nil {
		return "", err
	}
	tmp := fmt.Sprintf("%s.new.%d", exe, os.Getpid())
	if err := downloadFile(dlURL, tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("download failed: %w", err)
	}
	if err := verifyReleaseChecksum(rel, tmp, asset); err != nil {
		os.Remove(tmp)
		return "", err
	}
	os.Chmod(tmp, 0o755)
	if err := smokeTestBinary(tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	bak = exe + ".bak"
	if err := copyFile(exe, bak); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("cannot back up current binary: %w", err)
	}
	// rename(2) over exe is atomic: the path now names the new binary,
	// while a running process keeps its (now unlinked) old image.
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("swap failed, current binary untouched: %w", err)
	}
	return bak, nil
}

// copyFile copies src to dst (replacing it) with src's permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
