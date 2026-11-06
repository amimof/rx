// Package zsh holds the zsh widget printed by `rx init zsh`.
package zsh

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed rx.zsh
var script string

// Script returns the widget script with the rx binary path baked in.
func Script(bin string) string {
	return fmt.Sprintf("typeset -g _rx_bin=%s\n", shellQuote(bin)) + script
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
