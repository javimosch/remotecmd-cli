package main

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
)

func handleCP(args []string) {
	fs := flag.NewFlagSet("cp", flag.ExitOnError)
	target := fs.String("target", "", "target machine name")
	src := fs.String("src", "", "source path")
	dst := fs.String("dst", "", "destination path")
	stream := fs.Bool("stream", false, "stream progress as JSONL")
	fs.Parse(args)

	if *target == "" || *src == "" || *dst == "" {
		fmt.Fprintln(os.Stderr, "Error: --target, --src, and --dst are required")
		fmt.Fprintln(os.Stderr, "Usage: remotecmd-cli cp --target <name> --src <path> --dst <path> [--stream]")
		os.Exit(1)
	}

	if err := handleFileTransfer(*target, *src, *dst, *stream); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if !*stream {
		fmt.Printf("Copy completed successfully\n")
	}
}

func handleFileTransfer(target, src, dst string, stream bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %v", err)
	}
	if cfg.Relay.URL == "" {
		return fmt.Errorf("relay not configured. Run: remotecmd-cli set-relay --url <url> --name <name>")
	}

	tgt, ok := cfg.Targets[target]
	if !ok {
		return fmt.Errorf("unknown target %q. Run: remotecmd-cli add-target --name %s --token <token>", target, target)
	}

	// Auto-detect if source is directory or file
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %v", err)
	}

	var mode string
	var content string

	if stream {
		// Emit start event
		emitProgress("start", map[string]interface{}{
			"src":  src,
			"dst":  dst,
			"size": info.Size(),
			"type": mapType(info),
		})
	}

	if info.IsDir() {
		// Directory: use rsync mode with tar archive
		mode = "rsync"
		tarData, err := createTarArchive(src)
		if err != nil {
			return fmt.Errorf("creating tar archive: %v", err)
		}
		content = base64.StdEncoding.EncodeToString(tarData)
		if stream {
			emitProgress("archived", map[string]interface{}{
				"size": len(tarData),
			})
		}
	} else {
		// Single file: use scp mode
		mode = "scp"
		data, err := ioutil.ReadFile(src)
		if err != nil {
			return fmt.Errorf("reading source file: %v", err)
		}
		content = base64.StdEncoding.EncodeToString(data)
		if stream {
			emitProgress("read", map[string]interface{}{
				"size": len(data),
			})
		}
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("connecting to relay: %v", err)
	}
	defer conn.Close()

	// Resolve relay-registered name (may differ from local alias)
	relayTarget := target
	if tgt.RelayName != "" {
		relayTarget = tgt.RelayName
	}

	id := newID()
	msg := &Message{
		Type:    "file_transfer",
		ID:      id,
		Target:  relayTarget,
		Token:   tgt.Token,
		Mode:    mode, // May have been changed from rsync to scp for single files
		SrcPath: src,
		DstPath: dst,
		Content: content,
	}

	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("sending file_transfer request: %v", err)
	}

	if stream {
		emitProgress("sent", map[string]interface{}{
			"encoded_size": len(content),
		})
	}

	// Wait for response
	resultCh := make(chan *Message, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			var response Message
			if err := conn.ReadJSON(&response); err != nil {
				if stream {
					emitProgress("error", map[string]interface{}{
						"message": err.Error(),
					})
				}
				errCh <- err
				return
			}
			if response.Type == "result" && response.ID == id {
				if stream {
					emitProgress("complete", map[string]interface{}{
						"ok": response.OK,
					})
				}
				resultCh <- &response
				return
			}
		}
	}()

	select {
	case response := <-resultCh:
		if !*response.OK {
			return fmt.Errorf("file transfer failed: %s", response.Error)
		}
		return nil
	case err := <-errCh:
		return fmt.Errorf("connection error: %v", err)
	case <-time.After(30 * time.Second):
		if stream {
			emitProgress("timeout", map[string]interface{}{})
		}
		return fmt.Errorf("timeout waiting for file transfer result")
	}
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
