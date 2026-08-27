package mosh

import (
	"bytes"
	"strings"
	"testing"
)

func TestXTermTerminalOpenSequenceMatchesMoshFrontendContract(t *testing.T) {
	got := string(XTermTerminalOpenSequence())
	if got != "\x1b[?1049h\x1b[?1h" {
		t.Fatalf("unexpected open sequence: %q", got)
	}
}

func TestXTermTerminalCloseSequenceResetsInteractiveModes(t *testing.T) {
	got := string(XTermTerminalCloseSequence())
	for _, want := range []string{
		"\x1b[?1l", "\x1b[?25h", "\x1b[?2004l", "\x1b[?1004l",
		"\x1b[?1003l", "\x1b[?1002l", "\x1b[?1001l", "\x1b[?1000l",
		"\x1b[?1015l", "\x1b[?1006l", "\x1b[?1005l", "\x1b[?1049l",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("close sequence missing %q: %q", want, got)
		}
	}
}

func TestEnableXTermTerminalPresentationIsOrderedAndIdempotent(t *testing.T) {
	c := &Client{output: []byte("server-output"), outputC: make(chan struct{}, 1)}
	c.EnableXTermTerminalPresentation()
	c.EnableXTermTerminalPresentation()

	want := append(XTermTerminalOpenSequence(), []byte("server-output")...)
	if got := c.output; !bytes.Equal(got, want) {
		t.Fatalf("output=%q want=%q", got, want)
	}
}
