package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The terminal copy of the log, coloured so a question and its answer read as
// one block: a blank line and a cyan header open each question, the answer as
// printed in game follows in green with its rich text turned into terminal
// colour, and the per-round bookkeeping between them is dimmed. The log file
// never sees any of this. Colour only when stderr is a terminal and NO_COLOR
// is unset (https://no-color.org).

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiFg     = "\x1b[39m"
)

// logTerminal is where the log goes on screen: stderr, coloured when a person
// is watching it.
func logTerminal() io.Writer {
	if os.Getenv("NO_COLOR") != "" || !isTerminal(os.Stderr) {
		return os.Stderr
	}
	return colorWriter{os.Stderr}
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// colorWriter colours whole log entries. The standard logger hands Write one
// entry per call, timestamp first, so each call is coloured on its own.
type colorWriter struct{ w io.Writer }

func (c colorWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(c.w, colorEntry(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

var (
	logStamp      = regexp.MustCompile(`^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d `)
	logQuestion   = regexp.MustCompile(`^question (\d+) from (.*?): (.*)$`)
	logRendered   = regexp.MustCompile(`^answer (\d+) rendered:(.*)$`)
	logBookkeep   = regexp.MustCompile(`^(question \d+ round |answer \d+ (shape=|artifact )|idle, )`)
	logTrouble    = regexp.MustCompile(`(?i)\b(fail|failed|error|cannot|could not|giving up|refused|dropped)\b`)
	logWarning    = regexp.MustCompile(`WARNING`)
	richTag       = regexp.MustCompile(`\[(/?)([a-z-]+)(?:=([^\]]*))?\]`)
	richColorRGB  = regexp.MustCompile(`^\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)`)
	richNamedRGB  = map[string][3]int{"red": {255, 85, 85}, "green": {85, 255, 85}, "blue": {100, 140, 255}, "yellow": {255, 230, 85}, "orange": {255, 165, 0}, "white": {255, 255, 255}, "gray": {170, 170, 170}, "grey": {170, 170, 170}, "cyan": {85, 255, 255}, "pink": {255, 160, 200}, "purple": {190, 120, 255}}
	richIconTypes = map[string]bool{"img": true, "item": true, "entity": true, "technology": true, "recipe": true, "fluid": true, "virtual-signal": true, "tile": true, "planet": true, "space-location": true, "quality": true, "achievement": true, "item-group": true}
)

// colorEntry colours one log entry, trailing newline included.
func colorEntry(entry string) string {
	body := strings.TrimRight(entry, "\n")
	stamp := logStamp.FindString(body)
	msg := body[len(stamp):]
	first, rest, _ := strings.Cut(msg, "\n")
	when := ansiDim + stamp + ansiReset

	var out string
	switch {
	case logQuestion.MatchString(first):
		m := logQuestion.FindStringSubmatch(first)
		out = "\n" + when + ansiBold + ansiCyan + "Q" + m[1] + ansiReset + " " + ansiCyan + m[2] + ansiReset +
			"\n    " + ansiBold + m[3] + ansiReset
	case logRendered.MatchString(first):
		m := logRendered.FindStringSubmatch(first)
		out = when + ansiBold + ansiGreen + "A" + m[1] + ansiReset
		if tail := strings.TrimSpace(m[2]); tail != "" {
			out += " " + richToANSI(tail)
		}
		for _, line := range strings.Split(rest, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out += "\n    " + ansiGreen + "│" + ansiReset + " " + richToANSI(line)
			}
		}
		return out + "\n"
	case logBookkeep.MatchString(first):
		out = when + ansiDim + first + ansiReset
	case logWarning.MatchString(first):
		out = when + ansiYellow + first + ansiReset
	case logTrouble.MatchString(first):
		out = when + ansiRed + first + ansiReset
	default:
		out = when + first
	}
	if rest != "" {
		out += "\n" + rest
	}
	return out + "\n"
}

// richToANSI turns Factorio rich text into something a terminal shows:
// [color] becomes a true-colour foreground, an icon becomes its name in
// angle quotes, a map pin becomes its coordinates, and [font] drops out.
func richToANSI(s string) string {
	return richTag.ReplaceAllStringFunc(s, func(tag string) string {
		m := richTag.FindStringSubmatch(tag)
		closing, kind, arg := m[1] == "/", m[2], m[3]
		switch {
		case kind == "color" && closing:
			return ansiFg
		case kind == "color":
			if r, g, b, ok := richColor(arg); ok {
				return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
			}
			return ""
		case kind == "font":
			return ""
		case kind == "gps":
			return ansiCyan + "@" + arg + ansiFg
		case richIconTypes[kind] && !closing:
			name := arg
			if kind == "img" {
				if _, after, ok := strings.Cut(arg, "."); ok {
					name = after
				}
			}
			return ansiYellow + "‹" + name + "›" + ansiFg
		}
		return tag
	})
}

// richColor reads a Factorio colour argument: three components in 0..1, or
// in 0..255 when any of them is above 1, or one of the common colour names.
func richColor(arg string) (r, g, b int, ok bool) {
	if rgb, named := richNamedRGB[strings.TrimSpace(arg)]; named {
		return rgb[0], rgb[1], rgb[2], true
	}
	m := richColorRGB.FindStringSubmatch(arg)
	if m == nil {
		return 0, 0, 0, false
	}
	var v [3]float64
	scale := 255.0
	for i := range v {
		v[i], _ = strconv.ParseFloat(m[i+1], 64)
		if v[i] > 1 {
			scale = 1
		}
	}
	clamp := func(f float64) int { return max(0, min(255, int(f*scale+0.5))) }
	return clamp(v[0]), clamp(v[1]), clamp(v[2]), true
}
