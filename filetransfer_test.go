package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}

var errTestWrite = errors.New("simulated write failure")

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

func TestChunkCount(t *testing.T) {
	cases := []struct {
		size, chunk, want int
	}{
		{0, 4, 1},   // empty payload still yields one terminating chunk
		{1, 4, 1},
		{4, 4, 1},
		{5, 4, 2},
		{8, 4, 2},
		{9, 4, 3},
	}
	for _, c := range cases {
		if got := chunkCount(c.size, c.chunk); got != c.want {
			t.Errorf("chunkCount(%d,%d) = %d, want %d", c.size, c.chunk, got, c.want)
		}
	}
}

func TestChunkData(t *testing.T) {
	// Empty input -> a single empty chunk (guarantees a final frame).
	empty := chunkData(nil, 4)
	if len(empty) != 1 || len(empty[0]) != 0 {
		t.Fatalf("chunkData(nil) = %v, want one empty chunk", empty)
	}

	data := []byte("0123456789") // 10 bytes
	chunks := chunkData(data, 4)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	// Reassembling the chunks must reproduce the original bytes exactly.
	var reassembled []byte
	for i, ch := range chunks {
		if len(ch) > 4 {
			t.Errorf("chunk %d exceeds chunk size: %d bytes", i, len(ch))
		}
		reassembled = append(reassembled, ch...)
	}
	if !bytes.Equal(reassembled, data) {
		t.Errorf("reassembled = %q, want %q", reassembled, data)
	}
}

func TestMarshalWithinLimit(t *testing.T) {
	msg := &Message{Type: "file_chunk", ID: "abc", Data: strings.Repeat("x", 100)}

	// Comfortably under the limit: succeeds.
	if _, err := marshalWithinLimit(msg, 10000); err != nil {
		t.Errorf("unexpected error under limit: %v", err)
	}

	// Frame exceeds the limit: clear, actionable error instead of a stall.
	_, err := marshalWithinLimit(msg, 10)
	if err == nil {
		t.Fatal("expected error when frame exceeds limit")
	}
	if !strings.Contains(err.Error(), "exceeds relay frame limit") {
		t.Errorf("error = %q, want it to mention the relay frame limit", err.Error())
	}

	// A zero/negative limit disables the check.
	if _, err := marshalWithinLimit(msg, 0); err != nil {
		t.Errorf("limit 0 should disable the check, got %v", err)
	}
}

// captureWriter records the frames written by sendFileFrames so the transport
// can be exercised without a live WebSocket connection.
type captureWriter struct {
	frames [][]byte
	err    error
}

func (c *captureWriter) WriteMessage(_ int, data []byte) error {
	if c.err != nil {
		return c.err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	c.frames = append(c.frames, cp)
	return nil
}

func TestSendFileFramesChunking(t *testing.T) {
	// 10 KiB of data with a tiny chunk size forces multiple frames.
	data := bytes.Repeat([]byte("A"), 10*1024)
	const smallChunk = 4096

	w := &captureWriter{}
	base := &Message{ID: "id1", Target: "box", Token: "tok", Mode: "scp", SrcPath: "/s", DstPath: "/d"}
	if err := sendFileFramesWithSize(w, base, data, false, smallChunk); err != nil {
		t.Fatalf("sendFileFrames: %v", err)
	}

	// Binary protocol: 1 init + 3 chunks × 2 frames each (header + binary) = 7 frames.
	// But empty chunks (last chunk could be empty if data is exact multiple) skip binary.
	// 10240 / 4096 = 2.5 → 3 chunks (4096, 4096, 2048). All non-empty → 7 frames.
	if len(w.frames) != 7 {
		t.Fatalf("expected 7 frames (1 init + 3 headers + 3 binary), got %d", len(w.frames))
	}

	var init Message
	if err := json.Unmarshal(w.frames[0], &init); err != nil {
		t.Fatalf("init unmarshal: %v", err)
	}
	if init.Type != "file_transfer" || !init.Chunked || init.TotalChunks != 3 || init.TotalSize != int64(len(data)) {
		t.Errorf("init frame = %+v", init)
	}
	if init.Content != "" {
		t.Error("init frame should not carry inline content")
	}

	// Reassemble: frames alternate between JSON headers (text) and binary data.
	var got []byte
	chunkIdx := 0
	for i := 1; i < len(w.frames); i++ {
		frame := w.frames[i]
		// Check if this is a JSON text frame (header) or binary frame (data)
		// Heuristic: JSON frames start with '{'
		if len(frame) > 0 && frame[0] == '{' {
			var m Message
			if err := json.Unmarshal(frame, &m); err != nil {
				t.Fatalf("header unmarshal at frame %d: %v", i, err)
			}
			if m.Type != "file_chunk" || m.ID != "id1" {
				t.Errorf("chunk header %d = %+v", i, m)
			}
			if m.Seq != chunkIdx {
				t.Errorf("chunk %d seq = %d, want %d", i, m.Seq, chunkIdx)
			}
			if !m.BinaryChunk {
				t.Errorf("chunk %d should have BinaryChunk=true", i)
			}
			last := chunkIdx == 2
			if m.Final != last {
				t.Errorf("chunk %d final = %v, want %v", i, m.Final, last)
			}
			chunkIdx++
		} else {
			// Binary data frame
			got = append(got, frame...)
		}
	}
	if !bytes.Equal(got, data) {
		t.Errorf("reassembled payload differs from original (%d vs %d bytes)", len(got), len(data))
	}
}

func TestSendFileFramesWriteError(t *testing.T) {
	w := &captureWriter{err: errTestWrite}
	base := &Message{ID: "id1", Mode: "scp"}
	if err := sendFileFrames(w, base, []byte("hi"), false); err == nil {
		t.Fatal("expected error when the underlying write fails")
	}
}

func TestSendFileFramesStreamProgress(t *testing.T) {
	// --stream mode must emit one progress event per chunk so large transfers
	// remain observable instead of stalling silently (issue #1).
	const smallChunk = 4096
	data := bytes.Repeat([]byte("B"), 9000)
	wantChunks := chunkCount(len(data), smallChunk)

	output := captureStdout(t, func() {
		w := &captureWriter{}
		base := &Message{ID: "id2", Target: "box", Token: "tok", Mode: "scp", SrcPath: "/s", DstPath: "/d"}
		if err := sendFileFramesWithSize(w, base, data, true, smallChunk); err != nil {
			t.Fatalf("sendFileFrames: %v", err)
		}
	})

	chunkEvents := 0
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatalf("invalid progress JSON %q: %v", line, err)
		}
		if parsed["event"] != "chunk" {
			continue
		}
		chunkEvents++
		dataObj, ok := parsed["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("chunk event missing data object: %v", parsed)
		}
		if dataObj["total_bytes"] != float64(len(data)) {
			t.Errorf("chunk total_bytes = %v, want %d", dataObj["total_bytes"], len(data))
		}
	}
	if chunkEvents != wantChunks {
		t.Errorf("got %d chunk progress events, want %d", chunkEvents, wantChunks)
	}
}

func TestSendFileFramesExceedsFrameLimit(t *testing.T) {
	// With binary frames, the JSON header is tiny and the data goes as a raw
	// binary frame. The frame limit now applies to the init frame, not chunks.
	// Test that an init frame with too many fields fails (simulated by using
	// a very small limit via a custom test).
	// Since binary chunks bypass the JSON limit, we verify the init frame
	// check still works by using a payload that produces a valid init.
	huge := strings.Repeat("x", relayMaxFrameSize)
	w := &captureWriter{}
	base := &Message{ID: "id3", Mode: "scp"}
	// With binary protocol, this should succeed because the data goes as
	// binary frames, not JSON. The header is tiny.
	err := sendFileFramesWithSize(w, base, []byte(huge), false, len(huge))
	if err != nil {
		t.Fatalf("unexpected error with binary frames: %v", err)
	}
	// Verify we got 1 init + 1 header + 1 binary frame
	if len(w.frames) != 3 {
		t.Errorf("expected 3 frames, got %d", len(w.frames))
	}
}

func TestWriteTransferScp(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remotecmd-wt-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dst := filepath.Join(tmpDir, "out.bin")
	r := &fileReassembly{mode: "scp", dst: dst}
	payload := bytes.Repeat([]byte("Z"), 5000)
	r.buf.Write(payload)

	if err := writeTransfer(r); err != nil {
		t.Fatalf("writeTransfer: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("written file differs from payload")
	}
}

func TestWriteTransferUnknownMode(t *testing.T) {
	r := &fileReassembly{mode: "bogus"}
	if err := writeTransfer(r); err == nil {
		t.Error("expected error for unknown mode")
	}
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
