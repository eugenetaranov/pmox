package main

import (
	"encoding/json"
	"io"
)

// printJSON writes v to w as two-space-indented JSON followed by a
// newline — the format every `--output json` command emits.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
