// Package bash holds the bash widget printed by `rx init bash`.
package bash

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed rx.bash
var script string

// Options are the values baked into the widget script.
type Options struct {
	// Bin is the rx binary the widget runs.
	Bin string
	// Key is the default key binding in caret notation (^X^X) or readline
	// syntax (\C-x\C-x). RX_KEY set in the shell still overrides it.
	Key string
	// Log is the default log file, overridden by RX_LOG. Empty keeps the
	// widget's default.
	Log string
}

// Script returns the widget script with o baked in.
func Script(o Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "_rx_bin=%s\n", shellQuote(o.Bin))
	if o.Key != "" {
		fmt.Fprintf(&b, "_rx_key=%s\n", shellQuote(o.Key))
	}
	if o.Log != "" {
		fmt.Fprintf(&b, "_rx_log=%s\n", shellQuote(o.Log))
	}
	return b.String() + script
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
