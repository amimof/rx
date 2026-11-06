// Package plugins embeds the bundled YAML plugins.
package plugins

import "embed"

// FS holds the bundled plugin files.
//
//go:embed *.yaml
var FS embed.FS
