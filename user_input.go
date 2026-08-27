package mosh

const (
	userInputGround = iota
	userInputEscape
	userInputSS3
)

// userInputTranslator mirrors the reference mosh UserInput parser. The local
// terminal is kept in application-cursor mode, so cursor keys arrive as SS3.
// When the remote application is not using DECCKM, translate SS3 A-D back to
// normal ANSI cursor sequences before writing them to the remote PTY.
type userInputTranslator struct {
	state int
}

func (t *userInputTranslator) translate(input []byte, applicationCursorKeys bool) []byte {
	out := make([]byte, 0, len(input)+2)
	for _, b := range input {
		switch t.state {
		case userInputGround:
			out = append(out, b)
			if b == 0x1b {
				t.state = userInputEscape
			}
		case userInputEscape:
			if b == 'O' {
				t.state = userInputSS3
			} else {
				t.state = userInputGround
				out = append(out, b)
			}
		case userInputSS3:
			t.state = userInputGround
			if !applicationCursorKeys && b >= 'A' && b <= 'D' {
				out = append(out, '[', b)
			} else {
				out = append(out, 'O', b)
			}
		}
	}
	return out
}
