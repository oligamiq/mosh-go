package mosh

// XTermTerminalOpenSequence returns the local-terminal setup performed by a
// normal mosh frontend. Mosh owns the local display while connected: it enters
// the alternate screen and forces application-cursor-key mode. The server-side
// terminal state machine then translates SS3 cursor keys when the remote
// application is not using DECCKM.
func XTermTerminalOpenSequence() []byte {
	return []byte("\x1b[?1049h\x1b[?1h")
}

// XTermTerminalCloseSequence restores modes that mosh may have enabled on the
// local terminal. The mouse resets mirror upstream mosh's Display::close(); the
// focus/bracketed-paste resets make reuse by embedded terminal views safe.
func XTermTerminalCloseSequence() []byte {
	return []byte("\x1b[?1l\x1b[0m\x1b[?25h" +
		"\x1b[?2004l\x1b[?1004l" +
		"\x1b[?1003l\x1b[?1002l\x1b[?1001l\x1b[?1000l" +
		"\x1b[?1015l\x1b[?1006l\x1b[?1005l\x1b[?1049l")
}
