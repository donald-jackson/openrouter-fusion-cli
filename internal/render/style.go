// Package render turns a Consultation into human text, JSON, or a SKILL.md.
package render

import (
	"os"
	"strconv"
	"strings"
	"unicode"
)

// DefaultWidth is used when the terminal width cannot be determined. Staying stdlib-only
// means no ioctl-based size probe, so COLUMNS is the only hint available.
const DefaultWidth = 100

// Style controls terminal decoration.
type Style struct {
	Color bool
	Width int
}

// AutoStyle picks decoration from the environment: colour only when writing to a
// terminal and NO_COLOR is unset, and width from COLUMNS when it is sane.
func AutoStyle(f *os.File) Style {
	s := Style{Width: DefaultWidth}
	if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols >= 40 && cols <= 300 {
		s.Width = cols
	}
	if os.Getenv("NO_COLOR") != "" {
		return s
	}
	if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		s.Color = true
	}
	return s
}

// ANSI codes, applied only when Style.Color is set.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
)

func (s Style) paint(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return code + text + ansiReset
}

func (s Style) bold(t string) string   { return s.paint(ansiBold, t) }
func (s Style) dim(t string) string    { return s.paint(ansiDim, t) }
func (s Style) red(t string) string    { return s.paint(ansiRed, t) }
func (s Style) green(t string) string  { return s.paint(ansiGreen, t) }
func (s Style) yellow(t string) string { return s.paint(ansiYellow, t) }
func (s Style) blue(t string) string   { return s.paint(ansiBlue, t) }
func (s Style) cyan(t string) string   { return s.paint(ansiCyan, t) }

// wrap breaks text to fit width, prefixing every line with indent. Blank lines are
// preserved so markdown paragraphing survives, and lines inside fenced code blocks or
// that are already short pass through untouched.
func wrap(text string, width int, indent string) []string {
	limit := width - len(indent)
	if limit < 20 {
		limit = 20
	}

	var out []string
	var fenced bool
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			out = append(out, indent+line)
			continue
		}
		if fenced || strings.TrimSpace(line) == "" {
			out = append(out, strings.TrimRight(indent+line, " "))
			continue
		}
		// Keep any leading indentation of the source line (list items, quotes).
		lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		for _, wrapped := range wrapWords(strings.TrimLeft(line, " \t"), limit-len(lead)) {
			out = append(out, indent+lead+wrapped)
		}
	}
	return out
}

func wrapWords(line string, limit int) []string {
	if limit < 10 {
		limit = 10
	}
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' })
	if len(fields) == 0 {
		return []string{""}
	}

	var out []string
	var b strings.Builder
	for _, word := range fields {
		switch {
		case b.Len() == 0:
			b.WriteString(word)
		case runeLen(b.String())+1+runeLen(word) <= limit:
			b.WriteByte(' ')
			b.WriteString(word)
		default:
			out = append(out, b.String())
			b.Reset()
			b.WriteString(word)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// runeLen counts printable width approximately, ignoring combining marks. It is good
// enough for wrapping prose and avoids a dependency on x/text.
func runeLen(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.Is(unicode.Mn, r) {
			n++
		}
	}
	return n
}

// truncate shortens s to at most n runes, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	if n < 2 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
