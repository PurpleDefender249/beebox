// Package term handles terminal detection, colour and text layout. Colour
// is never the only carrier of meaning: every coloured status also has a
// symbol and a word, so output stays clear for colour-blind users, screen
// readers and plain logs.
package term

import (
	"os"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

type winsize struct {
	Row, Col, X, Y uint16
}

// Width returns the terminal width in columns, or def when unknown.
func Width(f *os.File, def int) int {
	var ws winsize
	// #nosec G103 -- standard TIOCGWINSZ ioctl on a valid fd
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno == 0 && ws.Col > 0 {
		return int(ws.Col)
	}
	return def
}

// Style renders text attributes, or nothing when colour is disabled.
type Style struct {
	Color bool // emit ANSI colour/bold
	Plain bool // avoid decorative glyphs (sparklines, box characters)
}

// Detect chooses a Style for f following the NO_COLOR convention
// (https://no-color.org), FORCE_COLOR, TERM=dumb, and BEEBOX_PLAIN.
func Detect(f *os.File) Style {
	s := Style{Color: IsTerminal(f)}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		s.Color = false
	}
	if os.Getenv("TERM") == "dumb" {
		s.Color = false
		s.Plain = true
	}
	if v := os.Getenv("FORCE_COLOR"); v != "" && v != "0" {
		s.Color = true
	}
	if v := os.Getenv("BEEBOX_PLAIN"); v != "" && v != "0" {
		s.Plain = true
	}
	if !IsTerminal(f) {
		s.Plain = true
	}
	return s
}

func (s Style) wrap(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Bold text.
func (s Style) Bold(t string) string { return s.wrap("1", t) }

// Dim text.
func (s Style) Dim(t string) string { return s.wrap("2", t) }

// Red text.
func (s Style) Red(t string) string { return s.wrap("31", t) }

// Green text.
func (s Style) Green(t string) string { return s.wrap("32", t) }

// Yellow text.
func (s Style) Yellow(t string) string { return s.wrap("33", t) }

// Cyan text.
func (s Style) Cyan(t string) string { return s.wrap("36", t) }

// Wrap word-wraps text to width, prefixing each line with indent.
func Wrap(text string, width int, indent string) string {
	if width < 20 {
		width = 20
	}
	avail := width - utf8.RuneCountInString(indent)
	if avail < 20 {
		avail = 20
	}
	var out strings.Builder
	for pi, para := range strings.Split(text, "\n") {
		if pi > 0 {
			out.WriteByte('\n')
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		lineLen := 0
		out.WriteString(indent)
		for i, w := range words {
			wl := utf8.RuneCountInString(w)
			if i > 0 && lineLen+1+wl > avail {
				out.WriteByte('\n')
				out.WriteString(indent)
				lineLen = 0
			} else if i > 0 {
				out.WriteByte(' ')
				lineLen++
			}
			out.WriteString(w)
			lineLen += wl
		}
	}
	return out.String()
}

// WrapHanging wraps text so the first line starts with first and later
// lines with rest (used for numbered steps and timestamped log lines).
func WrapHanging(text string, width int, first, rest string) string {
	if width < 30 {
		width = 30
	}
	words := strings.Fields(text)
	var out strings.Builder
	out.WriteString(first)
	col := utf8.RuneCountInString(first)
	start := col
	for i, w := range words {
		wl := utf8.RuneCountInString(w)
		if i > 0 && col+1+wl > width {
			out.WriteByte('\n')
			out.WriteString(rest)
			col = utf8.RuneCountInString(rest)
			start = col
		} else if i > 0 {
			out.WriteByte(' ')
			col++
		}
		// Break very long tokens (log lines often have no spaces).
		for col+wl > width && wl > width-start && width-col > 8 {
			r := []rune(w)
			n := width - col
			out.WriteString(string(r[:n]))
			out.WriteByte('\n')
			out.WriteString(rest)
			w = string(r[n:])
			wl = len(r) - n
			col = utf8.RuneCountInString(rest)
			start = col
		}
		out.WriteString(w)
		col += wl
	}
	return out.String()
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// Sparkline renders values (scaled between lo and hi) as block characters.
func Sparkline(vals []float64, lo, hi float64) string {
	if len(vals) == 0 || hi <= lo {
		return ""
	}
	var b strings.Builder
	for _, v := range vals {
		f := (v - lo) / (hi - lo)
		if f < 0 {
			f = 0
		}
		if f > 1 {
			f = 1
		}
		b.WriteRune(sparkRunes[int(f*float64(len(sparkRunes)-1)+0.5)])
	}
	return b.String()
}

// Pad right-pads s to n display columns.
func Pad(s string, n int) string {
	l := utf8.RuneCountInString(s)
	if l >= n {
		return s
	}
	return s + strings.Repeat(" ", n-l)
}
