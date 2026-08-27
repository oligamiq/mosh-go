package mosh

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRawClientAgainstRealMoshServerLeavesFrontendModesToClient(t *testing.T) {
	if _, err := exec.LookPath("mosh-server"); err != nil {
		t.Skip("mosh-server not installed")
	}

	client, cleanup := startRealMoshClientForTest(t, false)
	defer cleanup()

	client.Send([]byte("printf '\\033[?1h\\033[?1049h\\033[?1002h\\033[?1006h\\033[?1004h\\033[?1007h\\033[?2004h'\n"))

	var output strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if recv := client.Recv(250 * time.Millisecond); len(recv) > 0 {
			output.Write(recv)
		}
		got := output.String()
		if strings.Contains(got, "\x1b[?1002h") && strings.Contains(got, "\x1b[?1006h") &&
			strings.Contains(got, "\x1b[?2004h") {
			if strings.Contains(got, "\x1b[?1h") || strings.Contains(got, "\x1b[?1049h") {
				t.Fatalf("server unexpectedly emitted frontend-owned modes: %q", got)
			}
			return
		}
	}
	t.Fatalf("server display modes were not reproduced in client output: %q", output.String())
}

func TestTerminalClientAddsMoshFrontendModes(t *testing.T) {
	if _, err := exec.LookPath("mosh-server"); err != nil {
		t.Skip("mosh-server not installed")
	}
	client, cleanup := startRealMoshClientForTest(t, true)
	defer cleanup()

	first := client.Recv(time.Second)
	if !bytes.HasPrefix(first, XTermTerminalOpenSequence()) {
		t.Fatalf("terminal client did not open with mosh frontend modes: %q", first)
	}
}

func startRealMoshClientForTest(t *testing.T, terminalPresentation bool) (*Client, func()) {
	t.Helper()
	cmd := exec.Command("mosh-server", "new", "-p", "0", "-c", "256")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Skipf("mosh-server failed to start: %v", err)
	}
	buf := make([]byte, 4096)
	var output string
	for !strings.Contains(output, "MOSH CONNECT") {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			output += string(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	_ = cmd.Wait()
	port, key := parseMoshConnect(output)
	if port == 0 || key == "" {
		t.Fatalf("bad MOSH CONNECT: %q", output)
	}
	var client *Client
	if terminalPresentation {
		client, err = DialTerminal("127.0.0.1", port, key)
	} else {
		client, err = Dial("127.0.0.1", port, key)
	}
	if err != nil {
		t.Fatal(err)
	}
	client.Resize(80, 24)
	return client, func() { client.Close() }
}
