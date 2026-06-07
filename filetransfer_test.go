package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapType(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-maptype-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test directory
	dirInfo, err := os.Stat(tmpDir)
	if err != nil {
		t.Fatalf("stat tmp dir: %v", err)
	}
	if mapType(dirInfo) != "directory" {
		t.Errorf("expected 'directory', got %q", mapType(dirInfo))
	}

	// Test file
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("hello"), 0644)
	fileInfo, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatalf("stat tmp file: %v", err)
	}
	if mapType(fileInfo) != "file" {
		t.Errorf("expected 'file', got %q", mapType(fileInfo))
	}
}

func TestCreateTarArchive(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-tar-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test structure
	err = os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("content-a"), 0644)
	if err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("content-b-longer"), 0644)
	if err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	err = os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
	if err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	err = os.WriteFile(filepath.Join(tmpDir, "sub", "c.txt"), []byte("sub-content"), 0644)
	if err != nil {
		t.Fatalf("write sub/c.txt: %v", err)
	}

	tarData, err := createTarArchive(tmpDir)
	if err != nil {
		t.Fatalf("createTarArchive: %v", err)
	}

	if len(tarData) == 0 {
		t.Fatal("expected non-empty tar data")
	}

	// Verify tar content
	tr := tar.NewReader(bytes.NewReader(tarData))
	files := make(map[string]string)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read error: %v", err)
		}
		var content bytes.Buffer
		io.Copy(&content, tr)
		files[header.Name] = content.String()
	}

	if files["a.txt"] != "content-a" {
		t.Errorf("a.txt content = %q", files["a.txt"])
	}
	if files["b.txt"] != "content-b-longer" {
		t.Errorf("b.txt content = %q", files["b.txt"])
	}
	if files["sub/c.txt"] != "sub-content" {
		t.Errorf("sub/c.txt content = %q", files["sub/c.txt"])
	}
	if _, ok := files["."]; !ok {
		t.Log("tar may or may not include root dir entry")
	}
}

func TestCreateTarArchiveInvalidPath(t *testing.T) {
	_, err := createTarArchive("/nonexistent/path")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestExtractTarArchive(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-extract-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a tar in memory
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	entries := []struct {
		name     string
		content  string
		isDir    bool
	}{
		{"dir1/", "", true},
		{"dir1/file1.txt", "hello from file1", false},
		{"file2.txt", "file2 content here", false},
	}

	for _, e := range entries {
		if e.isDir {
			tw.WriteHeader(&tar.Header{
				Name:     e.name,
				Typeflag: tar.TypeDir,
				Mode:     0755,
			})
		} else {
			tw.WriteHeader(&tar.Header{
				Name:     e.name,
				Size:     int64(len(e.content)),
				Typeflag: tar.TypeReg,
				Mode:     0644,
			})
			tw.Write([]byte(e.content))
		}
	}
	tw.Close()

	// Extract to destination
	dstDir := filepath.Join(tmpDir, "output")
	err = extractTarArchive(buf.Bytes(), dstDir)
	if err != nil {
		t.Fatalf("extractTarArchive: %v", err)
	}

	// Verify
	data1, err := os.ReadFile(filepath.Join(dstDir, "dir1", "file1.txt"))
	if err != nil {
		t.Fatalf("read dir1/file1.txt: %v", err)
	}
	if string(data1) != "hello from file1" {
		t.Errorf("dir1/file1.txt = %q", string(data1))
	}

	data2, err := os.ReadFile(filepath.Join(dstDir, "file2.txt"))
	if err != nil {
		t.Fatalf("read file2.txt: %v", err)
	}
	if string(data2) != "file2 content here" {
		t.Errorf("file2.txt = %q", string(data2))
	}
}

func TestExtractTarArchiveSkipsSymlinks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-extract-sym-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add a regular file
	tw.WriteHeader(&tar.Header{
		Name:     "safe.txt",
		Size:     int64(4),
		Typeflag: tar.TypeReg,
		Mode:     0644,
	})
	tw.Write([]byte("safe"))

	// Add a symlink (should be skipped)
	tw.WriteHeader(&tar.Header{
		Name:     "link.txt",
		Linkname: "/etc/passwd",
		Typeflag: tar.TypeSymlink,
	})
	tw.Close()

	err = extractTarArchive(buf.Bytes(), tmpDir)
	if err != nil {
		t.Fatalf("extractTarArchive: %v", err)
	}

	// Verify safe file exists
	data, err := os.ReadFile(filepath.Join(tmpDir, "safe.txt"))
	if err != nil {
		t.Fatalf("safe.txt should exist: %v", err)
	}
	if string(data) != "safe" {
		t.Errorf("safe.txt = %q, want %q", string(data), "safe")
	}

	// Verify symlink was NOT created
	if _, err := os.Lstat(filepath.Join(tmpDir, "link.txt")); !os.IsNotExist(err) {
		t.Error("symlink should not have been created")
	}
}

func TestExtractTarArchiveHardlinks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-extract-hard-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	tw.WriteHeader(&tar.Header{
		Name:     "real.txt",
		Size:     int64(5),
		Typeflag: tar.TypeReg,
		Mode:     0644,
	})
	tw.Write([]byte("hello"))

	tw.WriteHeader(&tar.Header{
		Name:     "hardlink.txt",
		Linkname: "real.txt",
		Typeflag: tar.TypeLink,
	})
	tw.Close()

	err = extractTarArchive(buf.Bytes(), tmpDir)
	if err != nil {
		t.Fatalf("extractTarArchive: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(tmpDir, "hardlink.txt")); !os.IsNotExist(err) {
		t.Error("hardlink should not have been created")
	}
}

func TestCreateAndExtractRoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-roundtrip-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create origin structure
	os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("world"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "nested", "deep"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "nested", "a.txt"), []byte("nested-a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "nested", "deep", "b.txt"), []byte("deep-b"), 0644)

	// Create tar
	tarData, err := createTarArchive(tmpDir)
	if err != nil {
		t.Fatalf("createTarArchive: %v", err)
	}

	// Extract to new location
	extractDir := filepath.Join(tmpDir, "restored")
	err = extractTarArchive(tarData, extractDir)
	if err != nil {
		t.Fatalf("extractTarArchive: %v", err)
	}

	// Verify round trip
	checkFile := func(path, expected string) {
		data, err := os.ReadFile(filepath.Join(extractDir, path))
		if err != nil {
			t.Errorf("missing %s: %v", path, err)
			return
		}
		if strings.TrimSpace(string(data)) != expected {
			t.Errorf("%s = %q, want %q", path, string(data), expected)
		}
	}

	checkFile("hello.txt", "world")
	checkFile("nested/a.txt", "nested-a")
	checkFile("nested/deep/b.txt", "deep-b")
}

func TestBoolPtr(t *testing.T) {
	ptr := boolPtr(true)
	if ptr == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *ptr != true {
		t.Error("expected true")
	}

	ptr2 := boolPtr(false)
	if *ptr2 != false {
		t.Error("expected false")
	}
}
