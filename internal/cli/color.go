// Package cli is the interactive chat client.
package cli

import (
	"os"
	"strconv"
	"strings"
)

// enabled is false when NO_COLOR is set or stdout is not a terminal.
var enabled = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

func wrap(code, s string) string {
	if !enabled {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func Bold(s string) string    { return wrap("1", s) }
func Dim(s string) string     { return wrap("2", s) }
func Red(s string) string     { return wrap("31", s) }
func Green(s string) string   { return wrap("32", s) }
func Yellow(s string) string  { return wrap("33", s) }
func Blue(s string) string    { return wrap("34", s) }
func Magenta(s string) string { return wrap("35", s) }
func Cyan(s string) string    { return wrap("36", s) }

// WorkerColor gives each worker a stable color from its number (w1 -> 0).
func WorkerColor(id string, s string) string {
	palette := []func(string) string{Cyan, Magenta, Yellow, Green, Blue}
	idx := 0
	if n, err := strconv.Atoi(strings.TrimPrefix(id, "w")); err == nil && n >= 1 {
		idx = (n - 1) % len(palette)
	}
	return palette[idx](s)
}
