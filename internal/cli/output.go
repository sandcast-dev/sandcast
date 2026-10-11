package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// writeJSON prints one JSON document followed by a newline.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

// table prints rows as aligned columns with a header.
type table struct {
	tw *tabwriter.Writer
}

func newTable(w io.Writer, header ...string) *table {
	t := &table{tw: tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)}
	if len(header) > 0 {
		t.row(header...)
	}
	return t
}

func (t *table) row(cols ...string) {
	_, _ = fmt.Fprintln(t.tw, strings.Join(cols, "\t"))
}

func (t *table) flush() error { return t.tw.Flush() }

// keyValue prints "key: value" lines with aligned values.
func keyValue(w io.Writer, pairs ...string) error {
	t := newTable(w)
	for i := 0; i+1 < len(pairs); i += 2 {
		t.row(pairs[i]+":", pairs[i+1])
	}
	return t.flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func joinOrDash(list []string) string {
	if len(list) == 0 {
		return "-"
	}
	return strings.Join(list, ",")
}
