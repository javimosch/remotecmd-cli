package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// An inherited pipe that nobody writes to is the normal shape of stdin under
// an agent harness, CI, nohup, systemd and cron. Reading it to EOF waits
// forever, and since this runs before the request is built, the command never
// reaches the daemon - the tool looks hung rather than failing.
func TestIdlePipeDoesNotBlock(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // deliberately left open for the duration of the test

	done := make(chan []byte, 1)
	go func() { done <- readPipeWithGrace(r, 100*time.Millisecond) }()

	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("an idle pipe produced %q, want nil", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("readPipeWithGrace blocked on a pipe nobody was writing to")
	}
}

// The ordinary `echo data | rcx ...` case must keep working.
func TestPipedDataIsRead(t *testing.T) {
	got := readPipeWithGrace(strings.NewReader("hello stdin"), 100*time.Millisecond)
	if string(got) != "hello stdin" {
		t.Fatalf("got %q, want %q", got, "hello stdin")
	}
}

// Nothing may be truncated. Once the first byte proves somebody is writing,
// the rest is read without a deadline - so a payload far larger than any
// buffer, arriving in chunks, still arrives whole.
func TestLargeSlowPayloadIsNotTruncated(t *testing.T) {
	const chunks, size = 40, 8192
	want := bytes.Repeat([]byte("abcdefgh"), chunks*size/8)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	go func() {
		defer w.Close()
		for off := 0; off < len(want); off += size {
			end := min(off+size, len(want))
			if _, err := w.Write(want[off:end]); err != nil {
				return
			}
			// Slower than the grace period on purpose: the deadline applies
			// to the first byte only, never to the body.
			time.Sleep(2 * time.Millisecond)
		}
	}()

	got := readPipeWithGrace(r, 50*time.Millisecond)
	if len(got) != len(want) {
		t.Fatalf("read %d bytes, want %d — the body was truncated", len(got), len(want))
	}
	if !bytes.Equal(got, want) {
		t.Error("content differs from what was written")
	}
}

// A producer that takes a while to say anything is the one case the grace
// period can get wrong, so it is adjustable rather than fixed.
func TestSlowFirstByteWithinGraceIsRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go func() {
		time.Sleep(80 * time.Millisecond)
		_, _ = io.WriteString(w, "late")
		w.Close()
	}()
	if got := readPipeWithGrace(r, time.Second); string(got) != "late" {
		t.Fatalf("got %q, want %q", got, "late")
	}
}

// A zero grace is the explicit "I really am piping you something" mode, via
// REMOTECMD_STDIN_WAIT=0. It waits as long as it takes.
func TestZeroGraceWaitsIndefinitely(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, "eventually")
		w.Close()
	}()
	if got := readPipeWithGrace(r, 0); string(got) != "eventually" {
		t.Fatalf("got %q, want %q", got, "eventually")
	}
}

func TestStdinWaitIsConfigurable(t *testing.T) {
	t.Setenv(StdinWaitEnv, "1500ms")
	if got := stdinWait(); got != 1500*time.Millisecond {
		t.Fatalf("stdinWait() = %v, want 1.5s", got)
	}
	t.Setenv(StdinWaitEnv, "nonsense")
	if got := stdinWait(); got != stdinFirstByteWait {
		t.Fatalf("an unparseable value gave %v, want the default %v", got, stdinFirstByteWait)
	}
}
