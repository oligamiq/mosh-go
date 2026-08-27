package mosh

import (
	"fmt"
	"sync"

	"github.com/charmbracelet/x/ansi"
	vt "github.com/unixshells/vt-go"
)

var mouseReportingModes = []int{9, 1000, 1001, 1002, 1003}
var mouseEncodingModes = []int{1005, 1006, 1015, 1016}
var simpleClientModes = []int{1004, 1007, 2004}

type terminalClientModes struct {
	reporting []int
	encoding  []int
	simple    map[int]bool
}

func newTerminalClientModes() terminalClientModes {
	return terminalClientModes{simple: make(map[int]bool)}
}

func (m terminalClientModes) clone() terminalClientModes {
	out := newTerminalClientModes()
	out.reporting = append(out.reporting, m.reporting...)
	out.encoding = append(out.encoding, m.encoding...)
	for k, v := range m.simple {
		out.simple[k] = v
	}
	return out
}

func (m *terminalClientModes) set(mode ansi.Mode, enabled bool) {
	dec, ok := mode.(ansi.DECMode)
	if !ok {
		return
	}
	n := int(dec)
	switch {
	case containsMode(mouseReportingModes, n):
		m.reporting = updateOrderedMode(m.reporting, n, enabled)
	case containsMode(mouseEncodingModes, n):
		m.encoding = updateOrderedMode(m.encoding, n, enabled)
	case containsMode(simpleClientModes, n):
		m.simple[n] = enabled
	}
}

func containsMode(modes []int, target int) bool {
	for _, mode := range modes {
		if mode == target {
			return true
		}
	}
	return false
}

func updateOrderedMode(order []int, mode int, enabled bool) []int {
	out := make([]int, 0, len(order))
	for _, existing := range order {
		if existing != mode {
			out = append(out, existing)
		}
	}
	if enabled {
		out = append(out, mode)
	}
	return out
}

func sameModeOrder(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func appendDECMode(buf []byte, mode int, enabled bool) []byte {
	suffix := 'l'
	if enabled {
		suffix = 'h'
	}
	return append(buf, fmt.Sprintf("\x1b[?%d%c", mode, suffix)...)
}

func appendOrderedModeTransition(buf []byte, known, old, next []int) []byte {
	if sameModeOrder(old, next) {
		return buf
	}
	// Mouse reporting and encoding modes have fallback/precedence semantics.
	// Reset the category, then replay every active request in request order.
	for _, mode := range known {
		buf = appendDECMode(buf, mode, false)
	}
	for _, mode := range next {
		buf = appendDECMode(buf, mode, true)
	}
	return buf
}

func clientModeDiff(old, next terminalClientModes) []byte {
	var out []byte
	out = appendOrderedModeTransition(out, mouseReportingModes, old.reporting, next.reporting)
	out = appendOrderedModeTransition(out, mouseEncodingModes, old.encoding, next.encoding)
	for _, mode := range simpleClientModes {
		if old.simple[mode] != next.simple[mode] {
			out = appendDECMode(out, mode, next.simple[mode])
		}
	}
	return out
}

type recvTerminalState struct {
	fb    *Framebuffer
	modes terminalClientModes
	title string
}

type recvStateTracker struct {
	mu sync.Mutex

	cols, rows    int
	shadow        *vt.Emulator
	shadowState   uint64
	shadowModes   terminalClientModes
	cursorVisible bool
	shadowTitle   string
	bellCount     uint64
	canonical     bool

	states         map[uint64]*recvTerminalState
	displayed      *recvTerminalState
	displayedState uint64
}

func newRecvStateTracker(cols, rows int) *recvStateTracker {
	st := &recvStateTracker{cols: cols, rows: rows, states: make(map[uint64]*recvTerminalState)}
	st.resetShadow(cols, rows)
	initial := st.snapshot()
	st.states[0] = initial
	st.displayed = cloneRecvTerminalState(initial)
	return st
}

func (st *recvStateTracker) resetShadow(cols, rows int) {
	st.shadow = vt.NewEmulator(cols, rows)
	st.shadowModes = newTerminalClientModes()
	st.cursorVisible = true
	st.shadowTitle = ""
	st.shadow.SetCallbacks(vt.Callbacks{
		EnableMode:       func(mode ansi.Mode) { st.shadowModes.set(mode, true) },
		DisableMode:      func(mode ansi.Mode) { st.shadowModes.set(mode, false) },
		CursorVisibility: func(visible bool) { st.cursorVisible = visible },
		Title:            func(title string) { st.shadowTitle = title },
		Bell:             func() { st.bellCount++ },
	})
}

func (st *recvStateTracker) snapshot() *recvTerminalState {
	return &recvTerminalState{
		fb:    SnapshotEmulator(st.shadow, st.cursorVisible),
		modes: st.shadowModes.clone(),
		title: st.shadowTitle,
	}
}

func cloneFramebuffer(fb *Framebuffer) *Framebuffer {
	if fb == nil {
		return nil
	}
	out := &Framebuffer{W: fb.W, H: fb.H, CurX: fb.CurX, CurY: fb.CurY, CurVis: fb.CurVis, Cells: make([]Cell, len(fb.Cells))}
	copy(out.Cells, fb.Cells)
	return out
}

func cloneRecvTerminalState(state *recvTerminalState) *recvTerminalState {
	if state == nil {
		return nil
	}
	return &recvTerminalState{fb: cloneFramebuffer(state.fb), modes: state.modes.clone(), title: state.title}
}

func (st *recvStateTracker) restore(stateNum uint64, state *recvTerminalState) {
	st.resetShadow(state.fb.W, state.fb.H)
	_, _ = st.shadow.Write(canonicalRecoveryPrefix())
	_, _ = st.shadow.Write(state.fb.Diff(nil))
	_, _ = st.shadow.Write(clientModeDiff(newTerminalClientModes(), state.modes))
	st.shadowTitle = state.title
	st.shadowState = stateNum
}

func (st *recvStateTracker) apply(diff []byte, oldNum, newNum, throwawayNum uint64) []byte {
	st.mu.Lock()
	defer st.mu.Unlock()

	if _, exists := st.states[newNum]; exists {
		return nil
	}
	base := st.states[oldNum]
	if base == nil {
		// Transport only returns diffs whose oldNum it has received. Inventing a
		// blank base here would permanently erase unchanged cells, so fail closed.
		return nil
	}
	if st.shadowState != oldNum {
		st.restore(oldNum, base)
	}
	if st.shadow.Width() != st.cols || st.shadow.Height() != st.rows {
		st.shadow.Resize(st.cols, st.rows)
	}

	if len(diff) == 0 {
		next := cloneRecvTerminalState(base)
		if next.fb.W != st.cols || next.fb.H != st.rows {
			st.shadow.Resize(st.cols, st.rows)
			next = st.snapshot()
		}
		st.shadowState = newNum
		st.states[newNum] = next
		return st.advanceDisplay(oldNum, newNum, throwawayNum, next, nil, 0)
	}

	instrs, err := UnmarshalHostMessage(diff)
	if err != nil {
		return nil
	}
	var raw []byte
	bellBefore := st.bellCount
	for _, hi := range instrs {
		if len(hi.Hoststring) == 0 {
			continue
		}
		raw = append(raw, hi.Hoststring...)
		_, _ = st.shadow.Write(hi.Hoststring)
	}
	st.shadowState = newNum
	next := st.snapshot()
	st.states[newNum] = next
	return st.advanceDisplay(oldNum, newNum, throwawayNum, next, raw, st.bellCount-bellBefore)
}

func canonicalRecoveryPrefix() []byte {
	// Once state branches, stop relying on the local terminal's hidden VT state.
	// Normalize the modes that affect interpretation of our canonical CUP/text
	// redraws, while deliberately preserving Mosh's outer alt-screen/?1h envelope
	// and the separately tracked mouse/focus/paste modes.
	return []byte("\x0f\x1b(B\x1b[m\x1b[4l\x1b[20l\x1b[?5l\x1b[?6l\x1b[?7h\x1b[?69l\x1b[r")
}

func (st *recvStateTracker) advanceDisplay(oldNum, newNum, throwawayNum uint64, next *recvTerminalState, raw []byte, bells uint64) []byte {
	if throwawayNum > 0 {
		for n := range st.states {
			if n < throwawayNum && n != newNum {
				delete(st.states, n)
			}
		}
	}

	if newNum <= st.displayedState {
		return nil
	}
	if oldNum == st.displayedState && !st.canonical {
		st.displayed = cloneRecvTerminalState(next)
		st.displayedState = newNum
		return raw
	}

	var out []byte
	if !st.canonical {
		out = append(out, canonicalRecoveryPrefix()...)
		st.canonical = true
	}
	out = append(out, next.fb.Diff(st.displayed.fb)...)
	out = append(out, clientModeDiff(st.displayed.modes, next.modes)...)
	if st.displayed.title != next.title {
		out = append(out, ansi.SetWindowTitle(next.title)...)
	}
	for i := uint64(0); i < bells; i++ {
		out = append(out, '\a')
	}
	st.displayed = cloneRecvTerminalState(next)
	st.displayedState = newNum
	return out
}

func (st *recvStateTracker) resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	st.mu.Lock()
	st.cols, st.rows = cols, rows
	st.mu.Unlock()
}
