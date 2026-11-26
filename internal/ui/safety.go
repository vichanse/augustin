package ui

import (
	"io"
	"os"
	"strings"
	"unicode"
)

// SafeText preserves line breaks and tabs while removing terminal controls
// and Unicode formatting controls from untrusted display text.
func SafeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// Interactive reports a character-device output as a conservative terminal
// candidate. It is not a full isatty check.
func Interactive(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func ColorEnabled(w io.Writer, lookup func(string) (string, bool)) bool {
	noColor, _ := lookup("NO_COLOR")
	term, _ := lookup("TERM")
	return Interactive(w) && noColor == "" && term != "" && term != "dumb"
}
