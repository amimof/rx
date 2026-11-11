// Package config loads rx's settings from config.yaml in rx's config
// directory and from RX_* environment variables, which take precedence.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"

	"github.com/amimof/rx/internal/tui"
)

// DefaultKey is the widget's default key binding.
const DefaultKey = "^X^X"

// Config is rx's configuration.
type Config struct {
	// Key is the widget's key binding in the shell's syntax (RX_KEY).
	Key string
	// Log is the widget's log file (RX_LOG); empty for the widget default.
	Log string
	// Prompt is the symbol shown before the command in the editor
	// (RX_PROMPT); empty shows none.
	Prompt string
	// Keys binds the editor's actions. Actions the file doesn't mention
	// keep their defaults.
	Keys tui.Keymap
	// File is the config file that was read, or "" if there was none.
	File string
}

// Dir returns rx's config directory: $XDG_CONFIG_HOME/rx, or ~/.config/rx.
func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "rx")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "rx")
}

// Path returns the default config file.
func Path() string { return filepath.Join(Dir(), "config.yaml") }

// envName returns the environment variable for a setting, e.g. RX_KEY or
// RX_KEYS_NEXT_SLOT.
func envName(key string) string {
	return "RX_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
}

// Load reads file, or the default path if file is empty, and applies
// environment variables. Only a missing default file is not an error.
func Load(file string) (*Config, error) {
	explicit := file != ""
	if !explicit {
		file = Path()
	}
	v := viper.New()
	v.SetConfigFile(file)
	v.SetConfigType("yaml")
	defaults := tui.DefaultKeys()
	settings := []string{"key", "log", "prompt"}
	for a := range defaults {
		settings = append(settings, "keys."+a)
	}
	for _, s := range settings {
		_ = v.BindEnv(s, envName(s))
	}

	c := &Config{Key: DefaultKey, Prompt: tui.DefaultPrompt, Keys: defaults}
	if err := v.ReadInConfig(); err == nil {
		c.File = file
	} else if explicit || !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	if err := checkKnown(v, defaults); err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	if v.IsSet("key") {
		c.Key = v.GetString("key")
	}
	if v.IsSet("log") {
		c.Log = expandHome(v.GetString("log"))
	}
	if v.IsSet("prompt") {
		c.Prompt = v.GetString("prompt")
	}
	for a := range defaults {
		if k := "keys." + a; v.IsSet(k) {
			c.Keys[a] = keyList(v.Get(k))
		}
	}
	if err := c.Keys.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	return c, nil
}

// checkKnown reports settings in the file that rx doesn't know.
func checkKnown(v *viper.Viper, actions tui.Keymap) error {
	keys := v.AllKeys()
	sort.Strings(keys)
	for _, k := range keys {
		if k == "key" || k == "log" || k == "prompt" || k == "keys" {
			continue
		}
		a, ok := strings.CutPrefix(k, "keys.")
		if !ok {
			return fmt.Errorf("unknown setting %q", k)
		}
		if _, ok := actions[a]; !ok {
			return fmt.Errorf("keys: unknown action %q", a)
		}
	}
	return nil
}

// keyList converts a list from the file, or a comma or space separated
// string from the environment, to key names.
func keyList(v any) []string {
	var out []string
	switch v := v.(type) {
	case string:
		out = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
	case []any:
		for _, x := range v {
			out = append(out, fmt.Sprint(x))
		}
	case []string:
		out = v
	}
	return out
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}
