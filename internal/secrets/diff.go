package secrets

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
)

// Diff statuses.
const (
	Same      = "same"
	Differs   = "differs"
	OnlyLeft  = "only-left"
	OnlyRight = "only-right"
)

// DiffRow compares one secret/key between two sides. Left and Right are short
// fingerprints, never values.
type DiffRow struct {
	Secret string `json:"secret"`
	Key    string `json:"key"`
	Status string `json:"status"`
	Left   string `json:"left,omitempty"`
	Right  string `json:"right,omitempty"`
}

// NewFingerprinter returns a function mapping a value to a short fingerprint.
// The HMAC key is random per call, so fingerprints are comparable within one
// run (equal value => equal fingerprint) but cannot be brute-forced offline
// the way a plain hash of a low-entropy password can.
func NewFingerprinter() (func([]byte) string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return func(v []byte) string {
		m := hmac.New(sha256.New, key)
		m.Write(v)
		return hex.EncodeToString(m.Sum(nil))[:8]
	}, nil
}

// Diff compares the secret/key pairs of two sides. Missing entries count as
// absent. The same secret/key consumed in several places is compared once.
func Diff(left, right []Entry, fp func([]byte) string) []DiffRow {
	index := func(es []Entry) map[[2]string]string {
		m := map[[2]string]string{}
		for _, e := range es {
			if !e.Missing && e.Key != "" {
				m[[2]string{e.Secret, e.Key}] = fp(e.Value)
			}
		}
		return m
	}
	l, r := index(left), index(right)
	keys := map[[2]string]bool{}
	for k := range l {
		keys[k] = true
	}
	for k := range r {
		keys[k] = true
	}
	rows := make([]DiffRow, 0, len(keys))
	for k := range keys {
		lv, inL := l[k]
		rv, inR := r[k]
		row := DiffRow{Secret: k[0], Key: k[1], Left: lv, Right: rv}
		switch {
		case inL && inR && lv == rv:
			row.Status = Same
		case inL && inR:
			row.Status = Differs
		case inL:
			row.Status = OnlyLeft
		default:
			row.Status = OnlyRight
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Secret != rows[j].Secret {
			return rows[i].Secret < rows[j].Secret
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

// FilterDiffKey keeps rows whose key is key; empty keeps everything.
func FilterDiffKey(rows []DiffRow, key string) []DiffRow {
	if key == "" {
		return rows
	}
	var out []DiffRow
	for _, r := range rows {
		if r.Key == key {
			out = append(out, r)
		}
	}
	return out
}

// RenderDiff writes rows as "table" or "json". leftName/rightName label the sides.
func RenderDiff(w io.Writer, rows []DiffRow, format, leftName, rightName string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Left  string    `json:"left"`
			Right string    `json:"right"`
			Rows  []DiffRow `json:"rows"`
		}{leftName, rightName, rows})
	case "table":
		fmt.Fprintf(w, "left:  %s\nright: %s\n(fingerprints are per-run and never reveal values)\n\n", leftName, rightName)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SECRET\tKEY\tSTATUS\tLEFT\tRIGHT")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", dash(r.Secret), r.Key, r.Status, dash(r.Left), dash(r.Right))
		}
		return tw.Flush()
	}
	return fmt.Errorf("unknown output format %q", format)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
