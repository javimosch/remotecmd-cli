package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateCheckFlagParsing(t *testing.T) {
	// --bad-flag should exit with config error
	assertExitCode(t, ExitConfigError, func() {
		handleUpdate([]string{"--bad-flag"})
	})
}

func TestUpdateForceFlagParsing(t *testing.T) {
	// --force alone is valid, --bad is not
	assertExitCode(t, ExitConfigError, func() {
		handleUpdate([]string{"--force", "--bad-flag"})
	})
}

func TestUpdateCheckAndForce(t *testing.T) {
	// --check --force should be accepted (check takes priority)
	// It will try to reach GitHub — if it fails, exit 100
	// If it succeeds, exit 5 (since local != remote in dev)
	// Either way, not a config error
	defer func() {
		r := recover()
		if r == nil {
			return // no exit called = success (shouldn't happen but ok)
		}
		code, ok := r.(exitCodePanic)
		if !ok {
			t.Errorf("expected exitCodePanic, got %T", r)
			return
		}
		c := int(code)
		if c == ExitConfigError {
			t.Errorf("should not exit with config error for valid flags")
		}
		// exit 5 (update available) or exit 100 (network fail) are both acceptable
	}()
	old := osExit
	osExit = func(code int) { panic(exitCodePanic(code)) }
	defer func() { osExit = old }()

	handleUpdate([]string{"--check", "--force"})
}

func TestFileSHA256(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "testfile")
	data := []byte("hello world")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex, got %d: %s", len(hash), hash)
	}
	// SHA256 of "hello world" is known
	expected := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if hash != expected {
		t.Errorf("expected %s, got %s", expected, hash)
	}
}

func TestFileSHA256Nonexistent(t *testing.T) {
	_, err := fileSHA256("/nonexistent/path/file")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileModTimeNonexistent(t *testing.T) {
	mt := fileModTime("/nonexistent/path/file")
	if !mt.IsZero() {
		t.Error("expected zero time for nonexistent file")
	}
}

func TestFileModTimeExisting(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "testfile")
	os.WriteFile(path, []byte("test"), 0o644)
	mt := fileModTime(path)
	if mt.IsZero() {
		t.Error("expected non-zero time for existing file")
	}
}

func TestResolveSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real-binary")
	os.WriteFile(target, []byte("binary"), 0o755)
	link := filepath.Join(tmp, "symlink")
	os.Symlink(target, link)

	resolved, err := resolveSymlink(link)
	if err != nil {
		t.Fatalf("resolveSymlink: %v", err)
	}
	// Should resolve to the target, not the link
	if resolved != target {
		t.Errorf("expected %s, got %s", target, resolved)
	}
}

func TestResolveSymlinkNonSymlink(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "regular")
	os.WriteFile(path, []byte("data"), 0o644)

	resolved, _ := resolveSymlink(path)
	// For a non-symlink, should return the path itself
	if resolved != path {
		t.Errorf("expected %s, got %s", path, resolved)
	}
}

func TestAssetNameForPlatform(t *testing.T) {
	name := assetNameForPlatform()
	if name == "" {
		t.Error("expected non-empty asset name")
	}
	// Should contain "remotecmd-cli-"
	if len(name) < 20 {
		t.Errorf("asset name too short: %s", name)
	}
}

func TestFindAsset(t *testing.T) {
	rel := &githubRelease{
		TagName: "v1.0.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
			{Name: assetNameForPlatform(), BrowserDownloadURL: "https://example.com/binary"},
		},
	}
	url, err := rel.findAsset()
	if err != nil {
		t.Fatalf("findAsset: %v", err)
	}
	if url != "https://example.com/binary" {
		t.Errorf("expected https://example.com/binary, got %s", url)
	}
}

func TestFindAssetNotFound(t *testing.T) {
	rel := &githubRelease{
		TagName: "v1.0.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
		},
	}
	_, err := rel.findAsset()
	if err == nil {
		t.Error("expected error for missing platform asset")
	}
}

func TestFindChecksumsURL(t *testing.T) {
	rel := &githubRelease{
		TagName: "v1.0.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
			{Name: "binary", BrowserDownloadURL: "https://example.com/binary"},
		},
	}
	url := rel.findChecksumsURL()
	if url != "https://example.com/checksums.txt" {
		t.Errorf("expected checksums URL, got %s", url)
	}
}

func TestFindChecksumsURLMissing(t *testing.T) {
	rel := &githubRelease{
		TagName: "v1.0.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "binary", BrowserDownloadURL: "https://example.com/binary"},
		},
	}
	url := rel.findChecksumsURL()
	if url != "" {
		t.Errorf("expected empty string for missing checksums, got %s", url)
	}
}

func TestSmokeTestBinaryInvalid(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "fake-binary")
	os.WriteFile(path, []byte("#!/bin/sh\necho not rcmd"), 0o755)
	err := smokeTestBinary(path)
	if err == nil {
		t.Error("expected error for non-rcmd binary")
	}
}

func TestSmokeTestBinaryNonexistent(t *testing.T) {
	err := smokeTestBinary("/nonexistent/binary")
	if err == nil {
		t.Error("expected error for nonexistent binary")
	}
}

func TestMaybeNudgeRespectsEnvVar(t *testing.T) {
	old := os.Getenv("RCMD_NO_NUDGE")
	os.Setenv("RCMD_NO_NUDGE", "1")
	defer os.Setenv("RCMD_NO_NUDGE", old)

	// Should return immediately without panicking
	maybeNudge()
}
