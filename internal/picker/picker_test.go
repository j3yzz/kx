package picker

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var apps = []string{"payments", "payments-web", "mysql", "manshoor-backend-search-stage-web", "search"}

func TestFilter(t *testing.T) {
	cases := map[string][]string{
		"":         {"mysql", "search", "payments", "payments-web", "manshoor-backend-search-stage-web"}, // shortest first
		"pay":      {"payments", "payments-web"},
		"MYSQL":    {"mysql"},                                       // case-insensitive
		"msql":     {"mysql"},                                       // subsequence
		"search":   {"search", "manshoor-backend-search-stage-web"}, // tight + early beats late
		"zzz":      nil,
		"wsearch":  nil, // letters must appear in order
		"bksearch": {"manshoor-backend-search-stage-web"},
	}
	for q, want := range cases {
		got := Filter(apps, q)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", q, got, want)
		}
	}
}

func keys(m *Model, in string) (bool, string) { return m.Handle([]byte(in)) }

func TestHandle(t *testing.T) {
	m := &Model{Items: apps}
	if done, _ := keys(m, "pay"); done || m.Query != "pay" {
		t.Fatalf("typing: %+v", m)
	}
	keys(m, "\x7f") // backspace
	if m.Query != "pa" {
		t.Errorf("backspace: %q", m.Query)
	}
	keys(m, "y")
	keys(m, "\x1b[B") // down
	if m.Cursor != 1 {
		t.Errorf("down: cursor %d", m.Cursor)
	}
	keys(m, "\x1b[B") // wraps
	if m.Cursor != 0 {
		t.Errorf("wrap: cursor %d", m.Cursor)
	}
	keys(m, "\x0e")     // Ctrl-N
	keys(m, "\x10\x10") // Ctrl-P twice -> wraps to last
	if m.Cursor != 1 {
		t.Errorf("ctrl-p: cursor %d", m.Cursor)
	}
	if done, got := keys(m, "\r"); !done || got != "payments-web" {
		t.Errorf("enter: %v %q", done, got)
	}

	// typing resets the cursor; Enter with no match keeps going
	m = &Model{Items: apps, Cursor: 3}
	keys(m, "zzz")
	if m.Cursor != 0 {
		t.Error("cursor not reset")
	}
	if done, _ := keys(m, "\r"); done {
		t.Error("enter on no match must not finish")
	}

	for name, in := range map[string]string{"esc": "\x1b", "ctrl-c": "\x03", "ctrl-d": "\x04"} {
		if done, got := keys(&Model{Items: apps}, in); !done || got != "" {
			t.Errorf("%s should cancel: %v %q", name, done, got)
		}
	}
	// pasted text in one chunk, then Enter
	m = &Model{Items: apps}
	if done, got := keys(m, "mysql\r"); !done || got != "mysql" {
		t.Errorf("paste+enter: %v %q", done, got)
	}
	// unknown escape sequences are ignored, not typed
	m = &Model{Items: apps}
	keys(m, "\x1b[3~")
	if m.Query != "" {
		t.Errorf("escape leaked into query: %q", m.Query)
	}
}

func TestView(t *testing.T) {
	m := &Model{Items: apps, Query: "pay"}
	v := strings.Join(m.view("app"), "\n")
	for _, want := range []string{"app > pay", "▶ payments", "  payments-web"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	many := &Model{Items: make([]string, 25)}
	for i := range many.Items {
		many.Items[i] = strings.Repeat("a", i+1)
	}
	if v := strings.Join(many.view("x"), "\n"); !strings.Contains(v, "… 15 more") {
		t.Errorf("overflow note missing:\n%s", v)
	}
}

func TestViewScrollsWithCursor(t *testing.T) {
	var items []string
	for i := 0; i < 25; i++ {
		items = append(items, fmt.Sprintf("app%02d", i))
	}
	m := &Model{Items: items}
	for i := 0; i < 12; i++ {
		m.Handle([]byte("\x1b[B"))
	}
	v := strings.Join(m.view("t"), "\n")
	if !strings.Contains(v, "▶ app12") || !strings.Contains(v, "above") || strings.Contains(v, "app00\n") {
		t.Fatalf("cursor row not visible:\n%s", v)
	}
	m.Handle([]byte("\x1b[A")) // wraps back up
	m.Cursor = 0
	if v = strings.Join(m.view("t"), "\n"); !strings.Contains(v, "▶ app00") || !strings.Contains(v, "15 more") {
		t.Fatalf("top of list:\n%s", v)
	}
}
