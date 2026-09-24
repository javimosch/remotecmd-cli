package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRelease serves checksums.txt with the given body (status 200) or the
// given status, and returns a release pointing at it.
func fakeRelease(t *testing.T, status int, body string) *githubRelease {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	rel := &githubRelease{TagName: "v9.9.9"}
	rel.Assets = append(rel.Assets, struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{"checksums.txt", srv.URL + "/checksums.txt"})
	return rel
}

func writeBinary(t *testing.T, content string) (path, digest string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "bin.new")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func TestVerifyReleaseChecksumOK(t *testing.T) {
	path, digest := writeBinary(t, "good binary")
	for _, sep := range []string{"  ", " *"} { // sha256sum text and -b modes
		rel := fakeRelease(t, 200, "deadbeef  other-asset\n"+digest+sep+assetNameForPlatform()+"\n")
		if err := verifyReleaseChecksum(rel, path, assetNameForPlatform()); err != nil {
			t.Errorf("sep %q: expected success, got %v", sep, err)
		}
	}
}

func TestVerifyReleaseChecksumFailsClosed(t *testing.T) {
	path, digest := writeBinary(t, "good binary")
	_, otherDigest := writeBinary(t, "tampered binary")
	asset := assetNameForPlatform()

	cases := []struct {
		name    string
		rel     *githubRelease
		wantErr string
	}{
		{"no checksums asset", &githubRelease{TagName: "v9.9.9"}, "no checksums.txt"},
		{"checksums 404", fakeRelease(t, 404, ""), "cannot fetch checksums.txt"},
		{"platform missing", fakeRelease(t, 200, digest+"  some-other-platform\n"), "no entry for"},
		{"empty file", fakeRelease(t, 200, ""), "no entry for"},
		{"not a digest", fakeRelease(t, 200, "abc123  "+asset+"\n"), "not a sha256 digest"},
		{"hash mismatch", fakeRelease(t, 200, otherDigest+"  "+asset+"\n"), "hash mismatch"},
	}
	for _, tc := range cases {
		err := verifyReleaseChecksum(tc.rel, path, assetNameForPlatform())
		if err == nil {
			t.Errorf("%s: expected refusal, got nil", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: error %q, want it to mention %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestVerifyReleaseChecksumUnreachable(t *testing.T) {
	path, _ := writeBinary(t, "good binary")
	rel := fakeRelease(t, 200, "")
	rel.Assets[0].BrowserDownloadURL = "http://127.0.0.1:1/checksums.txt" // nothing listens on :1
	if err := verifyReleaseChecksum(rel, path, assetNameForPlatform()); err == nil || !strings.Contains(err.Error(), "cannot fetch") {
		t.Errorf("expected fetch failure refusal, got %v", err)
	}
}
