// Package fish holds the fish widget printed by `rx init fish`.
package fish

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed rx.fish
var script string

// Options are the values baked into the widget script.
type Options struct {
	// Bin is the rx binary the widget runs.
	Bin string
	// Key is the default key binding in caret notation (^X^X), fish
	// escapes (\cx\cx) or fish 4 key names. RX_KEY set in the shell still
	// overrides it.
	Key string
	// Log is the default log file, overridden by RX_LOG. Empty keeps the
	// widget's default.
	Log string
}

// Script returns the widget script with o baked in.
func Script(o Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "set -g _rx_bin %s\n", quote(o.Bin))
	if o.Key != "" {
		fmt.Fprintf(&b, "set -g _rx_key %s\n", quote(o.Key))
	}
	if o.Log != "" {
		fmt.Fprintf(&b, "set -g _rx_log %s\n", quote(o.Log))
	}
	return b.String() + script
}

// quote single-quotes s for fish, where \ and ' are escaped with a
// backslash inside single quotes.
func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(s) + "'"
}
