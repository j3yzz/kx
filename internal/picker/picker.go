// Package picker is a small type-to-filter list chooser for the terminal, used
// when a command is run without its app argument.
package picker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/term"
)

// ErrCancelled means the user backed out (Esc, Ctrl-C or Ctrl-D).
var ErrCancelled = errors.New("cancelled")

const maxShown = 10

// Filter returns the items containing the query's letters in order (a fuzzy
// subsequence match, case-insensitive), best first: tight matches that start
// early win; ties go to shorter items, then alphabetical.
func Filter(items []string, query string) []string {
	q := strings.ToLower(query)
	type hit struct {
		item  string
		score int
	}
	var hits []hit
	for _, it := range items {
		first, last, ok := span(strings.ToLower(it), q)
		if ok {
			hits = append(hits, hit{it, (last-first)*10 + first})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if len(a.item) != len(b.item) {
			return len(a.item) < len(b.item)
		}
		return a.item < b.item
	})
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.item
	}
	return out
}

// span finds q as a subsequence of s, preferring a contiguous occurrence.
func span(s, q string) (first, last int, ok bool) {
	if q == "" {
		return 0, 0, true
	}
	if i := strings.Index(s, q); i >= 0 {
		return i, i + len(q) - 1, true
	}
	qi := 0
	for i := 0; i < len(s) && qi < len(q); i++ {
		if s[i] == q[qi] {
			if qi == 0 {
				first = i
			}
			last = i
			qi++
		}
	}
	return first, last, qi == len(q)
}

// Model is the picker state, independent of any terminal so it can be tested.
type Model struct {
	Items  []string
	Query  string
	Cursor int
}

func (m *Model) Matches() []string { return Filter(m.Items, m.Query) }

// Handle applies one chunk of terminal input. It reports whether the picker is
// finished and, if so, what was chosen (empty chosen with done = cancelled).
func (m *Model) Handle(b []byte) (done bool, chosen string) {
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c == '\r' || c == '\n':
			if ms := m.Matches(); m.Cursor < len(ms) {
				return true, ms[m.Cursor]
			}
		case c == 3 || c == 4: // Ctrl-C, Ctrl-D
			return true, ""
		case c == 0x1b:
			if i+1 >= len(b) { // lone Esc
				return true, ""
			}
			if b[i+1] != '[' { // not a CSI sequence: ignore the rest of the chunk
				i = len(b)
				break
			}
			// CSI: parameters, then one final byte in 0x40-0x7e (A = up, B = down).
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			if j < len(b) {
				switch b[j] {
				case 'A':
					m.move(-1)
				case 'B':
					m.move(1)
				}
			}
			i = j
		case c == 0x10: // Ctrl-P
			m.move(-1)
		case c == 0x0e: // Ctrl-N
			m.move(1)
		case c == 0x7f || c == 0x08:
			if m.Query != "" {
				m.Query = m.Query[:len(m.Query)-1]
				m.Cursor = 0
			}
		case c >= 0x20 && c < 0x7f:
			m.Query += string(c)
			m.Cursor = 0
		}
	}
	return false, ""
}

func (m *Model) move(d int) {
	n := len(m.Matches())
	if n == 0 {
		m.Cursor = 0
		return
	}
	m.Cursor = (m.Cursor + d + n) % n
}

// view renders the prompt and a window of maxShown matches that scrolls to keep
// the cursor line (marked) visible.
func (m *Model) view(title string) []string {
	lines := []string{fmt.Sprintf("%s > %s", title, m.Query)}
	ms := m.Matches()
	start := 0
	if m.Cursor >= maxShown {
		start = m.Cursor - maxShown + 1
	}
	end := min(start+maxShown, len(ms))
	if start > 0 {
		lines = append(lines, fmt.Sprintf("  … %d above", start))
	}
	for i := start; i < end; i++ {
		mark := "  "
		if i == m.Cursor {
			mark = "▶ "
		}
		lines = append(lines, mark+ms[i])
	}
	if end < len(ms) {
		lines = append(lines, fmt.Sprintf("  … %d more", len(ms)-end))
	}
	if len(ms) == 0 {
		lines = append(lines, "  (no match)")
	}
	return lines
}

// Pick shows items and returns the one the user chooses. in must be a terminal.
func Pick(in *os.File, out io.Writer, title string, items []string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("nothing to choose from")
	}
	fd := int(in.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, old)

	m := &Model{Items: items}
	drawn := 0
	draw := func() {
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dA", drawn-1)
		}
		fmt.Fprint(out, "\r\x1b[J")
		lines := m.view(title)
		fmt.Fprint(out, strings.Join(lines, "\r\n"))
		drawn = len(lines)
	}
	clear := func() {
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dA\r\x1b[J", drawn-1)
		}
	}

	draw()
	buf := make([]byte, 64)
	for {
		n, err := in.Read(buf)
		if err != nil {
			clear()
			return "", ErrCancelled
		}
		if done, chosen := m.Handle(buf[:n]); done {
			clear()
			if chosen == "" {
				return "", ErrCancelled
			}
			return chosen, nil
		}
		draw()
	}
}
