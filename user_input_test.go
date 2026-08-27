package mosh

import (
	"bytes"
	"testing"
)

func TestUserInputTranslatorCursorModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  bool
		in   []byte
		want []byte
	}{
		{"normal-up", false, []byte("\x1bOA"), []byte("\x1b[A")},
		{"application-up", true, []byte("\x1bOA"), []byte("\x1bOA")},
		{"normal-left", false, []byte("\x1bOD"), []byte("\x1b[D")},
		{"non-cursor-ss3", false, []byte("\x1bOP"), []byte("\x1bOP")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tr userInputTranslator
			if got := tr.translate(tc.in, tc.app); !bytes.Equal(got, tc.want) {
				t.Fatalf("translate(%q)=%q want=%q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUserInputTranslatorPreservesStateAcrossPackets(t *testing.T) {
	var tr userInputTranslator
	var got []byte
	got = append(got, tr.translate([]byte("x\x1b"), false)...)
	got = append(got, tr.translate([]byte("O"), false)...)
	got = append(got, tr.translate([]byte("Ay"), false)...)
	if want := []byte("x\x1b[Ay"); !bytes.Equal(got, want) {
		t.Fatalf("split translate=%q want=%q", got, want)
	}
}

func TestUserInputTranslatorModeSampledWhenSS3Completes(t *testing.T) {
	var tr userInputTranslator
	got := append([]byte{}, tr.translate([]byte("\x1bO"), false)...)
	got = append(got, tr.translate([]byte("A"), true)...)
	if want := []byte("\x1bOA"); !bytes.Equal(got, want) {
		t.Fatalf("mode-switch translate=%q want=%q", got, want)
	}
}
