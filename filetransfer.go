package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const (
	// fileChunkSize is the raw size of each file transfer chunk.
	// 2 MiB — benchmarked optimal on high-RTT relay links (77ms RTT
	// through dk1). Smaller chunks allow better pipelining: the relay's
	// async write queue can forward chunk N to the target while the
	// client sends chunk N+1. With 8 MiB chunks, only 2 chunks fit in a
	// 10 MiB transfer, limiting overlap. 2 MiB gives 5 chunks for 10 MiB,
	// keeping the pipeline full.
	fileChunkSize = 2 << 20 // 2 MiB

	// relayMaxFrameSize is the maximum WebSocket frame the relay will accept.
	relayMaxFrameSize = 32 << 20 // 32 MiB

	// compressionThreshold is the ratio below which we use the compressed version.
	compressionThreshold = 0.9

	// compressionSampleSize is the number of bytes to test-compress as a
	// quick heuristic. If the sample doesn't compress well, skip the full
	// chunk — avoids wasting CPU on incompressible data (random binary,
	// already-compressed files, encrypted data).
	compressionSampleSize = 4096
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
		osExit(ExitConfigError)
	}

	if err := handleFileTransfer(*target, *src, *dst, *stream); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(ExitConfigError)
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

	if stream {
		// Emit start event
		emitProgress("start", map[string]interface{}{
			"src":  src,
			"dst":  dst,
			"size": info.Size(),
			"type": mapType(info),
		})
	}

	// For directories, we still need to create the tar archive in memory.
	// For single files, we stream directly from disk to avoid loading
	// the entire file into memory.
	var tarData []byte
	var fileReader *os.File
	if info.IsDir() {
		mode = "rsync"
		tarData, err = createTarArchive(src)
		if err != nil {
			return fmt.Errorf("creating tar archive: %v", err)
		}
		if stream {
			emitProgress("archived", map[string]interface{}{
				"size": len(tarData),
			})
		}
	} else {
		mode = "scp"
		fileReader, err = os.Open(src)
		if err != nil {
			return fmt.Errorf("opening source file: %v", err)
		}
		defer fileReader.Close()
	}

	u := wsURL(cfg.Relay.URL)
	conn, _, err := dialRelay(u)
	if err != nil {
		return fmt.Errorf("connecting to relay: %v", err)
	}
	defer conn.Close()
	// Set generous write deadline so large chunks don't timeout on slow links
	conn.SetWriteDeadline(time.Now().Add(10 * time.Minute))

	// Resolve relay-registered name (may differ from local alias)
	relayTarget := target
	if tgt.RelayName != "" {
		relayTarget = tgt.RelayName
	}

	id := newID()
	base := &Message{
		ID:      id,
		Target:  relayTarget,
		Token:   tgt.Token,
		Mode:    mode,
		SrcPath: src,
		DstPath: dst,
	}

	// Send the payload — streaming from disk for single files, from memory for tar
	chunkSz := effectiveChunkSize()

	// Parallel streams: auto-enable for files over scp mode.
	// Override with RCMD_PARALLEL_STREAMS env var (0 = disabled, N = N streams).
	// Auto-tuning based on file size (CERN/GridFTP research: 2-10 streams
	// optimal, linear gain up to ~10 streams, diminishing returns beyond):
	//   5-20 MiB  → 2 streams
	//   20-100 MiB → 3 streams
	//   100+ MiB  → 4 streams
	parallelStreams := 0
	if s := os.Getenv("RCMD_PARALLEL_STREAMS"); s != "" {
		fmt.Sscanf(s, "%d", &parallelStreams)
	} else if fileReader != nil && mode == "scp" {
		size := info.Size()
		switch {
		case size > 100*1024*1024:
			parallelStreams = 4
		case size > 20*1024*1024:
			parallelStreams = 3
		case size > 5*1024*1024:
			parallelStreams = 2
		}
	}
	if parallelStreams > 1 && fileReader != nil && mode == "scp" {
		conn.Close()
		return sendFileParallel(cfg, relayTarget, tgt.Token, src, dst, info.Size(), stream, chunkSz, parallelStreams)
	}

	var totalSent int64
	var totalChunks int
	if fileReader != nil {
		totalSent, totalChunks, err = sendFileStreaming(conn, base, fileReader, info.Size(), stream, chunkSz)
	} else {
		totalChunks = chunkCount(len(tarData), chunkSz)
		err = sendFileFramesWithSize(conn, base, tarData, stream, chunkSz)
		totalSent = int64(len(tarData))
	}
	if err != nil {
		if stream {
			emitProgress("error", map[string]interface{}{
				"message": err.Error(),
			})
		}
		return err
	}

	if stream {
		emitProgress("sent", map[string]interface{}{
			"total_bytes": totalSent,
			"chunks":      totalChunks,
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
	case <-time.After(10 * time.Minute):
		if stream {
			emitProgress("timeout", map[string]interface{}{})
		}
		return fmt.Errorf("timeout waiting for file transfer result")
	}
}
