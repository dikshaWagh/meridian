package tui

import (
	"os"
	"strconv"
	"strings"
)

var enabled = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, e := os.Stdout.Stat()
	return e == nil && f.Mode()&os.ModeCharDevice != 0
}()

func wrap(c, s string) string {
	if !enabled {
		return s
	}
	return "\033[" + c + "m" + s + "\033[0m"
}
func Bold(s string) string    { return wrap("1", s) }
func Dim(s string) string     { return wrap("2", s) }
func Red(s string) string     { return wrap("31", s) }
func Green(s string) string   { return wrap("32", s) }
func Yellow(s string) string  { return wrap("33", s) }
func Blue(s string) string    { return wrap("34", s) }
func Magenta(s string) string { return wrap("35", s) }
func Cyan(s string) string    { return wrap("36", s) }
func WorkerColor(id, s string) string {
	p := []func(string) string{Cyan, Magenta, Yellow, Green, Blue}
	i := 0
	if n, e := strconv.Atoi(strings.TrimPrefix(id, "w")); e == nil && n > 0 {
		i = (n - 1) % len(p)
	}
	return p[i](s)
}
