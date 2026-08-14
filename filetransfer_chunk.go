package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// effectiveChunkSize returns the chunk size, optionally overridden by the
// RCMD_CHUNK_SIZE environment variable (in bytes) for benchmarking.
func effectiveChunkSize() int {
	if s := os.Getenv("RCMD_CHUNK_SIZE"); s != "" {
		var n int
		fmt.Sscanf(s, "%d", &n)
		if n > 0 {
			return n
		}
	}
	return fileChunkSize
}

// compressPool reuses gzip writer buffers across chunks to reduce GC pressure.
var compressPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// gzipWriterPool reuses gzip.Writer objects across chunks.
// gzip.Writer is ~40KB, so avoiding allocation per chunk is important.
var gzipWriterPool = sync.Pool{
	New: func() interface{} {
		gz, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
		return gz
	},
}

// tryCompress gzip-compresses data and returns the compressed version if it's
// smaller than the original by at least 10%. Uses a sample-based heuristic to
// skip incompressible data quickly without compressing the full chunk.
func tryCompress(data []byte) []byte {
	if len(data) < 1024 {
		return nil // too small to bother
	}

	// Quick heuristic: compress a small sample first. If it doesn't compress
	// well, the full chunk almost certainly won't either — skip it.
	sampleEnd := compressionSampleSize
	if sampleEnd > len(data) {
		sampleEnd = len(data)
	}
	sample := data[:sampleEnd]
	var sampleBuf bytes.Buffer
	sgz, _ := gzip.NewWriterLevel(&sampleBuf, gzip.BestSpeed)
	sgz.Write(sample)
	sgz.Close()
	// If the sample compressed to >= 95% of original, the data is likely
	// incompressible — don't waste time compressing the full chunk.
	if float64(sampleBuf.Len()) >= float64(len(sample))*0.95 {
		return nil
	}

	// Sample compressed well — compress the full chunk.
	buf := compressPool.Get().(*bytes.Buffer)
	buf.Reset()
	gz := gzipWriterPool.Get().(*gzip.Writer)
	gz.Reset(buf)
	if _, err := gz.Write(data); err != nil {
		gz.Close()
		gzipWriterPool.Put(gz)
		compressPool.Put(buf)
		return nil
	}
	if err := gz.Close(); err != nil {
		gz.Reset(nil) // reset for reuse
		gzipWriterPool.Put(gz)
		compressPool.Put(buf)
		return nil
	}
	// Put gzip writer back in pool for reuse (after Close, it's safe to reset)
	gz.Reset(nil)
	gzipWriterPool.Put(gz)

	if float64(buf.Len()) < float64(len(data))*compressionThreshold {
		result := make([]byte, buf.Len())
		copy(result, buf.Bytes())
		compressPool.Put(buf)
		return result
	}
	compressPool.Put(buf)
	return nil
}

func mapType(info os.FileInfo) string {
	if info.IsDir() {
		return "directory"
	}
	return "file"
}

func createTarArchive(srcPath string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.Walk(srcPath, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}

		// Adjust header name to be relative to source
		relPath, err := filepath.Rel(srcPath, file)
		if err != nil {
			return err
		}
		header.Name = relPath

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// If not a directory, write file content
		if !fi.IsDir() {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if _, err := tw.Write(data); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// chunkCount returns the number of chunks a payload of size bytes is split
// into for a given chunk size. An empty payload still yields one (empty)
// chunk so the receiver always sees a terminating "final" frame.
func chunkCount(size, chunkSize int) int {
	if size <= 0 {
		return 1
	}
	return (size + chunkSize - 1) / chunkSize
}

// chunkData splits data into slices of at most chunkSize bytes. An empty
// input yields a single empty chunk so that a terminating frame is always
// emitted (see chunkCount).
func chunkData(data []byte, chunkSize int) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var chunks [][]byte
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}
	return chunks
}

// marshalWithinLimit marshals msg to JSON and fails with a clear error if the
// resulting frame would exceed the relay's frame limit. This turns the old
// silent stall on oversized payloads into an actionable error.
func marshalWithinLimit(msg *Message, limit int) ([]byte, error) {
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(b) > limit {
		return nil, fmt.Errorf("message frame of %d bytes exceeds relay frame limit of %d bytes (reduce chunk size)", len(b), limit)
	}
	return b, nil
}

// frameWriter is the subset of *websocket.Conn used to send frames, so the
// transport can be exercised in tests without a live connection.
type frameWriter interface {
	WriteMessage(messageType int, data []byte) error
}
