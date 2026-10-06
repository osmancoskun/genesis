package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func clearScreen() {
	// ANSI home + erase — no subprocess.
	fmt.Print("\033[H\033[2J")
}

func pauseEnter(in *bufio.Reader) {
	fmt.Print("\nPress Enter to continue…")
	_, _ = in.ReadString('\n')
}

func newAligned() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
}

type menuRow struct {
	key   string
	label string
	desc  string
}

func printMenuTable(title string, rows []menuRow) {
	fmt.Println(title)
	fmt.Println(strings.Repeat("-", len(title)))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		if r.desc == "" {
			fmt.Fprintf(w, "  %s)\t%s\t\n", r.key, r.label)
		} else {
			fmt.Fprintf(w, "  %s)\t%s\t%s\n", r.key, r.label, r.desc)
		}
	}
	_ = w.Flush()
}

func printChoices(label string, vals []string) {
	fmt.Println(label)
	fmt.Print(formatChoiceGrid(vals, 2))
	fmt.Println()
}

// formatChoiceGrid lays out numbered choices column-major (top→bottom, then next column right).
func formatChoiceGrid(vals []string, cols int) string {
	if len(vals) == 0 {
		return "  (none)\n"
	}
	if cols < 1 {
		cols = 1
	}
	n := len(vals)
	rows := (n + cols - 1) / cols
	cells := make([]string, n)
	cellW := 0
	for i, v := range vals {
		cells[i] = fmt.Sprintf("%d) %s", i+1, v)
		if len(cells[i]) > cellW {
			cellW = len(cells[i])
		}
	}
	var b strings.Builder
	for r := 0; r < rows; r++ {
		b.WriteString("  ")
		for c := 0; c < cols; c++ {
			i := c*rows + r
			if i >= n {
				continue
			}
			b.WriteString(cells[i])
			if c < cols-1 {
				b.WriteString(strings.Repeat(" ", cellW-len(cells[i])+4))
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func resolveChoice(input string, vals []string, def string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(input, "%d", &n); err == nil && n >= 1 && n <= len(vals) {
		return vals[n-1]
	}
	return input
}

var (
	choicesPins            = []string{"auto", "dry-run", "netlink", "noop"}
	choicesDefaultPathMode = []string{"auto", "dry-run", "netlink", "off"}
)

func validPinsMode(m string) bool {
	for _, v := range choicesPins {
		if m == v {
			return true
		}
	}
	return false
}

func validDefaultPathMode(m string) bool {
	for _, v := range choicesDefaultPathMode {
		if m == v {
			return true
		}
	}
	return false
}
