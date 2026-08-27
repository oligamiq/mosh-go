package mosh

import (
	"bytes"
	"strings"
	"testing"

	vt "github.com/unixshells/vt-go"
)

func hostStateDiff(s string) []byte {
	return MarshalHostMessage([]HostInstruction{{Hoststring: []byte(s), EchoAckNum: -1}})
}

func TestRecvStateTrackerLinearPathPreservesRawHostOutput(t *testing.T) {
	st := newRecvStateTracker(20, 4)
	raw := "\x1b]0;title\x07hello\x1b[?2004h"
	got := st.apply(hostStateDiff(raw), 0, 1, 0)
	if !bytes.Equal(got, []byte(raw)) {
		t.Fatalf("linear output changed: got %q want %q", got, raw)
	}
}

func TestRecvStateTrackerRecoversBranchFromStoredBase(t *testing.T) {
	st := newRecvStateTracker(10, 3)
	display := vt.NewEmulator(10, 3)

	first := st.apply(hostStateDiff("\x1b[1;1HA"), 0, 1, 0)
	if len(first) == 0 {
		t.Fatal("missing first output")
	}
	_, _ = display.Write(first)

	recovered := st.apply(hostStateDiff("\x1b[1;1HB"), 0, 2, 0)
	if len(recovered) == 0 {
		t.Fatal("missing branch recovery output")
	}
	_, _ = display.Write(recovered)

	cell := display.CellAt(0, 0)
	if cell == nil || cell.Content != "B" {
		t.Fatalf("display did not recover branch: cell=%v", cell)
	}
}

func TestRecvStateTrackerReplaysMouseRequestsInOrderOnRecovery(t *testing.T) {
	st := newRecvStateTracker(10, 3)
	_ = st.apply(hostStateDiff("\x1b[?1000h"), 0, 1, 0)

	recovered := string(st.apply(hostStateDiff("\x1b[?1002h\x1b[?1003h\x1b[?1006h"), 0, 2, 0))
	if recovered == "" {
		t.Fatal("missing recovery output")
	}
	resetIdx := strings.Index(recovered, "\x1b[?1003l")
	buttonIdx := strings.Index(recovered, "\x1b[?1002h")
	anyIdx := strings.Index(recovered, "\x1b[?1003h")
	if resetIdx < 0 || buttonIdx < 0 || anyIdx < 0 {
		t.Fatalf("mouse recovery missing transitions: %q", recovered)
	}
	if !(resetIdx < buttonIdx && buttonIdx < anyIdx) {
		t.Fatalf("mouse request order not preserved: %q", recovered)
	}
	if !strings.Contains(recovered, "\x1b[?1006h") {
		t.Fatalf("mouse encoding not restored: %q", recovered)
	}
}

func TestRecvStateTrackerRestoresSimpleClientModes(t *testing.T) {
	st := newRecvStateTracker(10, 3)
	_ = st.apply(hostStateDiff("\x1b[?1004h\x1b[?2004h"), 0, 1, 0)

	recovered := string(st.apply(hostStateDiff("\x1b[?1004l\x1b[?2004h"), 0, 2, 0))
	if !strings.Contains(recovered, "\x1b[?1004l") {
		t.Fatalf("focus mode not reset on recovery: %q", recovered)
	}
	if strings.Contains(recovered, "\x1b[?2004l") {
		t.Fatalf("bracketed paste was incorrectly disabled on recovery: %q", recovered)
	}
}

func TestRecvStateTrackerTracksNoOpStateNumbers(t *testing.T) {
	st := newRecvStateTracker(10, 3)
	if got := st.apply(nil, 0, 1, 0); got != nil {
		t.Fatalf("no-op state produced output: %q", got)
	}
	if st.states[1] == nil || st.displayedState != 1 {
		t.Fatalf("no-op state was not tracked: displayed=%d", st.displayedState)
	}
	got := st.apply(hostStateDiff("X"), 1, 2, 0)
	if !bytes.Equal(got, []byte("X")) {
		t.Fatalf("state after no-op was not linear: %q", got)
	}
}

func TestRecvStateTrackerRejectsUnknownBaseInsteadOfInventingBlankState(t *testing.T) {
	st := newRecvStateTracker(10, 3)
	if got := st.apply(hostStateDiff("X"), 99, 100, 0); got != nil {
		t.Fatalf("unknown base unexpectedly produced output: %q", got)
	}
	if _, exists := st.states[100]; exists {
		t.Fatal("unknown base created a synthetic state")
	}
}

func TestRecvStateTrackerResizeUsesCurrentTerminalSize(t *testing.T) {
	st := newRecvStateTracker(10, 3)
	st.resize(20, 5)
	_ = st.apply(hostStateDiff("\x1b[H\x1b[2Jwide"), 0, 1, 0)
	state := st.states[1]
	if state == nil || state.fb.W != 20 || state.fb.H != 5 {
		t.Fatalf("snapshot size = %v, want 20x5", state)
	}
}

func TestRecvStateTrackerCanonicalizesAllOutputAfterBranchRecovery(t *testing.T) {
	st := newRecvStateTracker(10, 4)
	display := vt.NewEmulator(10, 4)

	// Poison hidden terminal interpretation state on the initially displayed branch.
	first := st.apply(hostStateDiff("\x1b[2;3r\x1b[?6h\x1b[4h\x1b[1;1HA"), 0, 1, 0)
	_, _ = display.Write(first)

	recovered := st.apply(hostStateDiff("\x1b[1;1HB"), 0, 2, 0)
	if !st.canonical {
		t.Fatal("branch recovery did not enter canonical mode")
	}
	for _, reset := range []string{"\x1b[4l", "\x1b[?6l", "\x1b[r"} {
		if !strings.Contains(string(recovered), reset) {
			t.Fatalf("recovery missing interpretation reset %q: %q", reset, recovered)
		}
	}
	_, _ = display.Write(recovered)

	// Even though this is now a linear SSP transition, keep canonicalizing: the
	// local terminal no longer promises to share hidden scroll/mode state with server.
	next := st.apply(hostStateDiff("\x1b[1;2HC"), 2, 3, 0)
	_, _ = display.Write(next)
	if cell := display.CellAt(0, 0); cell == nil || cell.Content != "B" {
		t.Fatalf("recovered cell 0 = %#v, want B", cell)
	}
	if cell := display.CellAt(1, 0); cell == nil || cell.Content != "C" {
		t.Fatalf("canonical follow-up cell 1 = %#v, want C", cell)
	}
}

func TestRecvStateTrackerCanonicalRecoveryPreservesTitleAndBell(t *testing.T) {
	st := newRecvStateTracker(10, 3)
	_ = st.apply(hostStateDiff("\x1b[1;1HA"), 0, 1, 0)
	recovered := st.apply(hostStateDiff("\x1b]2;branch-title\x1b\\\a\x1b[1;1HB"), 0, 2, 0)
	if !strings.Contains(string(recovered), "branch-title") {
		t.Fatalf("canonical recovery lost title update: %q", recovered)
	}
	if !bytes.Contains(recovered, []byte{'\a'}) {
		t.Fatalf("canonical recovery lost bell: %q", recovered)
	}
}
