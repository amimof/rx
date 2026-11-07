// Command rx edits shell commands as a set of typed slots.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/config"
	"github.com/amimof/rx/internal/history"
	"github.com/amimof/rx/internal/shell"
	"github.com/amimof/rx/internal/shell/proto"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/plugins"
)

// widget is set when rx runs under a shell widget, whose stderr is the log.
var widget bool

// cfgFile is the --config flag.
var cfgFile string

var errCancelled = errors.New("cancelled")

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "rx",
		Short: "Edit shell commands as slots",
		Long: `rx edits shell commands as slots: arguments and flags you change from a
list of suggestions taken from history, shell completion and plugins.

Settings are read from ` + config.Path() + `, and RX_* environment
variables override them.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default "+config.Path()+")")
	root.AddCommand(newInitCmd(), newEditCmd(), newIndexCmd(), newCompleteCmd(), newPluginsCmd(), newDoctorCmd())
	return root
}

func main() {
	err := newRoot().Execute()
	if errors.Is(err, errCancelled) {
		os.Exit(1)
	}
	if err != nil {
		// Under the widget stderr is the log file, so stamp the line.
		if widget {
			fmt.Fprint(os.Stderr, time.Now().Format(time.RFC3339), " ")
		}
		fmt.Fprintln(os.Stderr, "rx:", err)
		os.Exit(1)
	}
}

// addShellFlag registers --shell on cmd.
func addShellFlag(cmd *cobra.Command) *string {
	return cmd.Flags().String("shell", "", fmt.Sprintf("shell rx runs under: %s (default: basename of $SHELL if supported, else %s)",
		strings.Join(shell.Names(), ", "), shell.Default))
}

// env holds everything loaded at startup.
type env struct {
	sh      *shell.Shell
	reg     *spec.Registry
	aliases spec.Aliases
	hist    *history.Index
	cls     *spec.Classifier
}

func cacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "rx")
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "rx")
	}
	return ""
}

func histFile(sh *shell.Shell) string {
	for _, v := range []string{"RX_HISTFILE", "HISTFILE"} {
		if f := os.Getenv(v); f != "" {
			return f
		}
	}
	return sh.HistFile()
}

// parseAliases decodes RX_ALIASES: escaped "name=value" lines.
func parseAliases(s string) spec.Aliases {
	a := spec.Aliases{}
	for _, l := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(proto.Unescape(l), "="); ok && k != "" {
			a[k] = v
		}
	}
	return a
}

func loadRegistry() (*spec.Registry, error) {
	reg := spec.NewRegistry()
	if err := reg.LoadFS(plugins.FS, "bundled:"); err != nil {
		return nil, err
	}
	if err := reg.LoadDir(filepath.Join(config.Dir(), "plugins")); err != nil {
		return nil, err
	}
	return reg, nil
}

func loadEnv(sh *shell.Shell, withHistory bool) (*env, error) {
	reg, err := loadRegistry()
	if err != nil {
		return nil, err
	}
	e := &env{sh: sh, reg: reg, aliases: parseAliases(os.Getenv("RX_ALIASES"))}
	e.cls = &spec.Classifier{Specs: reg, Aliases: e.aliases}
	if withHistory {
		x, err := history.Load(histFile(sh), cacheDir(), e.salt(), sh.ReadHistory, *e.cls)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "rx: history:", err)
		}
		e.hist = x
		if x != nil {
			e.cls.Infer = x
		}
	}
	return e, nil
}

// salt changes whenever specs, aliases or the shell (and so the history
// format) change, invalidating the history index, since all of them affect
// how history lines are read and classified.
func (e *env) salt() string {
	data, _ := json.Marshal(struct {
		Shell string
		S     []*spec.Spec
		A     spec.Aliases
	}{e.sh.Name, e.reg.Specs(), e.aliases})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
