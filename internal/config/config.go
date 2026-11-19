// Package config loads rx's settings from config.yaml in rx's config
// directory and from RX_* environment variables, which take precedence.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/viper"

	"github.com/amimof/rx/internal/spec"
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
	// Complete lists the completion sources enabled by default
	// (RX_COMPLETE). Plugins may replace it; nil enables all of them.
	Complete spec.Sources
	// Rules is when plugin rules run (RX_RULES): tui.RulesLive,
	// tui.RulesCommit or tui.RulesOff.
	Rules string
	// Focus is the focus for commands whose plugin sets none (RX_FOCUS):
	// spec.FocusLast to start on the slot last edited, or "" for the
	// default.
	Focus string
	// FocusLast remembers the slot last edited per command, for focus
	// lists with spec.FocusLast (RX_FOCUS_LAST). Off, those entries are
	// skipped and nothing is remembered.
	FocusLast bool
	// Keys binds the editor's actions. Actions the file doesn't mention
	// keep their defaults.
	Keys tui.Keymap
	// Theme styles the editor. Roles the file doesn't mention keep their
	// defaults.
	Theme tui.Theme
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
	settings := []string{"key", "log", "prompt", "complete", "rules", "focus", "focus_last"}
	for a := range defaults {
		settings = append(settings, "keys."+a)
	}
	for _, s := range settings {
		_ = v.BindEnv(s, envName(s))
	}

	c := &Config{Key: DefaultKey, Prompt: tui.DefaultPrompt, Rules: tui.RulesLive, FocusLast: true, Keys: defaults, Theme: tui.DefaultTheme()}
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
	if v.IsSet("complete") {
		s, err := spec.ParseSources(v.Get("complete"))
		if err == nil {
			err = errors.Join(s.Validate()...)
		}
		if err != nil {
			return nil, fmt.Errorf("config %s: complete: %w", file, err)
		}
		c.Complete = s
	}
	if v.IsSet("rules") {
		c.Rules = v.GetString("rules")
		if !slices.Contains(tui.RulesModes, c.Rules) {
			return nil, fmt.Errorf("config %s: rules: want one of %s, got %q", file, strings.Join(tui.RulesModes, ", "), c.Rules)
		}
	}
	if v.IsSet("focus") {
		c.Focus = v.GetString("focus")
		if c.Focus != "" && c.Focus != spec.FocusLast {
			return nil, fmt.Errorf("config %s: focus: want %s or nothing, got %q", file, spec.FocusLast, c.Focus)
		}
	}
	if v.IsSet("focus_last") {
		// YAML gives a bool, the environment a string.
		s := fmt.Sprint(v.Get("focus_last"))
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("config %s: focus_last: want true or false, got %q", file, s)
		}
		c.FocusLast = b
	}
	for a := range defaults {
		if k := "keys." + a; v.IsSet(k) {
			c.Keys[a] = keyList(v.Get(k))
		}
	}
	if err := c.Keys.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	if v.IsSet("theme") {
		if err := loadTheme(c.Theme, v.Get("theme")); err != nil {
			return nil, fmt.Errorf("config %s: %w", file, err)
		}
	}
	if err := c.Theme.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	return c, nil
}

// loadTheme sets the roles in v, the file's theme section, in t. A role is
// a map of attributes, or a string that sets only its foreground color.
func loadTheme(t tui.Theme, v any) error {
	roles, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("theme: want a map of roles, got %T", v)
	}
	names := make([]string, 0, len(roles))
	for r := range roles {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, r := range names {
		if _, ok := t[r]; !ok {
			return fmt.Errorf("theme: unknown role %q", r)
		}
		s, err := themeStyle(roles[r])
		if err != nil {
			return fmt.Errorf("theme.%s: %w", r, err)
		}
		t[r] = s
	}
	return nil
}

func themeStyle(v any) (tui.Style, error) {
	var s tui.Style
	switch v := v.(type) {
	case nil:
		return s, nil
	case string:
		s.Fg = v
		return s, nil
	case int:
		s.Fg = fmt.Sprint(v)
		return s, nil
	case map[string]any:
		for attr, x := range v {
			if attr == "fg" || attr == "bg" {
				c := fmt.Sprint(x)
				if attr == "fg" {
					s.Fg = c
				} else {
					s.Bg = c
				}
				continue
			}
			var flag *bool
			switch attr {
			case "bold":
				flag = &s.Bold
			case "italic":
				flag = &s.Italic
			case "faint":
				flag = &s.Faint
			case "underline":
				flag = &s.Underline
			case "reverse":
				flag = &s.Reverse
			default:
				return s, fmt.Errorf("unknown attribute %q", attr)
			}
			b, ok := x.(bool)
			if !ok {
				return s, fmt.Errorf("%s: want true or false, got %v", attr, x)
			}
			*flag = b
		}
		return s, nil
	}
	return s, fmt.Errorf("want a color or a map of attributes, got %v", v)
}

// checkKnown reports settings in the file that rx doesn't know.
func checkKnown(v *viper.Viper, actions tui.Keymap) error {
	keys := v.AllKeys()
	sort.Strings(keys)
	for _, k := range keys {
		// The complete and theme sections are checked as they are loaded.
		if k == "key" || k == "log" || k == "prompt" || k == "rules" || k == "focus" || k == "focus_last" || k == "keys" ||
			k == "complete" || strings.HasPrefix(k, "complete.") || k == "theme" || strings.HasPrefix(k, "theme.") {
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
