package mosh

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestServerTracksApplicationCursorMode(t *testing.T) {
	s := &Server{cols: 80, rows: 24}
	s.initializeEmulator()
	if s.applicationCursorKeys.Load() {
		t.Fatal("cursor application mode should start disabled")
	}
	s.emu.Write([]byte("\x1b[?1h"))
	if !s.applicationCursorKeys.Load() {
		t.Fatal("?1h did not enable cursor application mode")
	}
	s.emu.Write([]byte("\x1b[?1l"))
	if s.applicationCursorKeys.Load() {
		t.Fatal("?1l did not disable cursor application mode")
	}
}

func TestTerminalClientGoServerCursorModeTranslation(t *testing.T) {
	srv, err := NewServer("", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	outR, outW := io.Pipe()
	inR, inW := io.Pipe()
	rw := struct {
		io.Reader
		io.Writer
		io.Closer
	}{outR, inW, outR}
	go srv.ServeRW(&rw, nil)
	defer func() {
		outR.Close()
		outW.Close()
		inR.Close()
		inW.Close()
		srv.Close()
	}()

	client, err := DialTerminal("127.0.0.1", srv.Port(), srv.KeyBase64())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.Resize(80, 24)

	waitForCursorMode(t, srv, false)
	client.Send([]byte("\x1bOA"))
	if got := readPipeBytes(t, inR, 3); !bytes.Contains(got, []byte("\x1b[A")) {
		t.Fatalf("normal cursor mode got %q, want ESC [ A", got)
	}

	if _, err := outW.Write([]byte("\x1b[?1h")); err != nil {
		t.Fatal(err)
	}
	waitForCursorMode(t, srv, true)
	client.Send([]byte("\x1bOA"))
	if got := readPipeBytes(t, inR, 3); !bytes.Contains(got, []byte("\x1bOA")) {
		t.Fatalf("application cursor mode got %q, want ESC O A", got)
	}

	if _, err := outW.Write([]byte("\x1b[?1l")); err != nil {
		t.Fatal(err)
	}
	waitForCursorMode(t, srv, false)
	client.Send([]byte("\x1bOD"))
	if got := readPipeBytes(t, inR, 3); !bytes.Contains(got, []byte("\x1b[D")) {
		t.Fatalf("restored normal cursor mode got %q, want ESC [ D", got)
	}
}

func waitForCursorMode(t *testing.T, srv *Server, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.applicationCursorKeys.Load() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("application cursor mode did not become %v", want)
}

func readPipeBytes(t *testing.T, r *io.PipeReader, min int) []byte {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := r.Read(buf)
		ch <- result{append([]byte(nil), buf[:n]...), err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.data) < min {
			t.Fatalf("short pipe read %q", got.data)
		}
		return got.data
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for translated keystrokes")
		return nil
	}
}

func TestServeRWCloseStopsServer(t *testing.T) {
	srv, err := NewServer("", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	outR, outW := io.Pipe()
	inR, inW := io.Pipe()
	rw := struct {
		io.Reader
		io.Writer
		io.Closer
	}{outR, inW, outR}
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.ServeRW(&rw, nil) }()

	client, err := Dial("127.0.0.1", srv.Port(), srv.KeyBase64())
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	srv.Close()

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("ServeRW returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeRW did not stop after Close")
	}
	outW.Close()
	inR.Close()
	inW.Close()
}
