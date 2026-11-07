// Package zsh holds the zsh widget printed by `rx init zsh`.
package zsh

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed rx.zsh
var script string

// Options are the values baked into the widget script.
type Options struct {
	// Bin is the rx binary the widget runs.
	Bin string
	// Key is the default key binding in bindkey syntax. RX_KEY set in the
	// shell still overrides it.
	Key string
	// Log is the default log file, overridden by RX_LOG. Empty keeps the
	// widget's default.
	Log string
}

// Script returns the widget script with o baked in.
func Script(o Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "typeset -g _rx_bin=%s\n", shellQuote(o.Bin))
	if o.Key != "" {
		fmt.Fprintf(&b, "typeset -g _rx_key=%s\n", shellQuote(o.Key))
	}
	if o.Log != "" {
		fmt.Fprintf(&b, "typeset -g _rx_log=%s\n", shellQuote(o.Log))
	}
	return b.String() + script
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
