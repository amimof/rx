// Command rx edits shell commands as a set of typed slots.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/engine"
	"github.com/amimof/rx/internal/history"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/shell"
	"github.com/amimof/rx/internal/shell/proto"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/internal/tui"
	"github.com/amimof/rx/plugins"
)

const usage = `rx - edit shell commands as slots

Usage:
  rx init SHELL                     print the shell widget (zsh: eval "$(rx init zsh)")
  rx edit [--shell SHELL] [--cursor N] -- BUFFER
                                    open the slot editor; --shell is set by the
                                    widget and makes rx talk to it on stdin/stdout
  rx index [--shell SHELL] [--show CMD [SLOT]]
                                    build the history index and show candidates
  rx complete [--shell SHELL] [--slot KEY] -- CMD
                                    print completions for a slot (debugging)
  rx plugins list                   list loaded plugins
  rx plugins lint [FILE...]         validate plugin files
  rx doctor [--shell SHELL]         check the setup (in zsh, run rx-doctor for
                                    key bindings too)

SHELL is one of: %s. Without --shell, the basename of $SHELL is used if
supported, otherwise %s.
`

func printUsage(w *os.File) {
	fmt.Fprintf(w, usage, strings.Join(shell.Names(), ", "), shell.Default)
}

// widget is set when rx runs under a shell widget, whose stderr is the log.
var widget bool

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "init":
		err = cmdInit(args)
	case "edit":
		err = cmdEdit(args)
	case "index":
		err = cmdIndex(args)
	case "complete":
		err = cmdComplete(args)
	case "plugins":
		err = cmdPlugins(args)
	case "doctor":
		err = cmdDoctor(args)
	case "-h", "--help", "help":
		printUsage(os.Stdout)
	default:
		printUsage(os.Stderr)
		os.Exit(2)
	}
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

var errCancelled = errors.New("cancelled")

func cmdInit(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: rx init %s", strings.Join(shell.Names(), "|"))
	}
	sh, ok := shell.Lookup(args[0])
	if !ok {
		return fmt.Errorf("unsupported shell %q (supported: %s)", args[0], strings.Join(shell.Names(), ", "))
	}
	bin, err := os.Executable()
	if err != nil {
		bin = "rx"
	}
	fmt.Print(sh.Script(bin))
	return nil
}

// shellFlag registers --shell on fs.
func shellFlag(fs *flag.FlagSet) *string {
	return fs.String("shell", "", "shell rx runs under: "+strings.Join(shell.Names(), ", "))
}

// env holds everything loaded at startup.
type env struct {
	sh      *shell.Shell
	reg     *spec.Registry
	aliases spec.Aliases
	hist    *history.Index
	cls     *spec.Classifier
}

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "rx")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "rx")
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
	if err := reg.LoadDir(filepath.Join(configDir(), "plugins")); err != nil {
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

func cmdEdit(args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	shellName := shellFlag(fs)
	cursor := fs.Int("cursor", -1, "cursor position in the shell's unit (default: end)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	buf := strings.Join(fs.Args(), " ")

	// With --shell, rx was started by the widget and talks to it on stdin
	// and stdout.
	var client *proto.Client
	if *shellName != "" {
		widget = true
		client = proto.NewClient(*shellName, os.Stdin, os.Stdout)
	}
	cancel := func() error {
		if client != nil {
			_ = client.Cancel()
		}
		return errCancelled
	}
	// fail reports err to the widget, which shows it below the prompt. It is
	// also returned so it lands in the log.
	fail := func(err error) error {
		if client != nil {
			_ = client.Error(err.Error())
		}
		return err
	}
	sh, err := shell.Resolve(*shellName)
	if err != nil {
		return fail(err)
	}

	pos := len(buf)
	if *cursor >= 0 {
		pos = sh.ToByte(buf, *cursor)
	}
	line := parse.Parse(buf)
	seg := line.SegmentAt(pos)
	if seg < 0 {
		return fail(errors.New("nothing to edit"))
	}

	e, err := loadEnv(sh, true)
	if err != nil {
		return fail(err)
	}
	cmd := e.cls.Classify(line, seg)
	sp := e.reg.Lookup(cmd.Name)

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fail(err)
	}
	defer tty.Close()

	// Only a non-nil client may become the engine's Shell: a nil
	// *proto.Client in the interface would not compare equal to nil.
	var live engine.Shell
	if client != nil {
		live = client
	}
	eng := engine.New(live, &complete.Cache{Dir: filepath.Join(cacheDir(), "complete")})
	out, err := tui.Run(tui.Options{Cmd: cmd, Spec: sp, History: e.hist, Engine: eng, Infer: e.hist, TTY: tty})
	if err != nil {
		return fail(fmt.Errorf("editor: %w", err))
	}
	if out.Cancelled {
		return cancel()
	}
	rendered := out.Cmd.Render()
	result := line.Splice(seg, rendered)
	end := sh.FromByte(result, line.Segments[seg].Start+len(rendered))
	if client != nil {
		return client.Done(result, end)
	}
	fmt.Println(result)
	return nil
}

func cmdIndex(args []string) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	shellName := shellFlag(fs)
	show := fs.String("show", "", "command to show candidates for")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sh, err := shell.Resolve(*shellName)
	if err != nil {
		return err
	}
	start := time.Now()
	e, err := loadEnv(sh, true)
	if err != nil {
		return err
	}
	if e.hist == nil {
		return fmt.Errorf("no history at %s", histFile(sh))
	}
	fmt.Printf("indexed %s (%d bytes) in %v\n", histFile(sh), e.hist.Size, time.Since(start).Round(time.Millisecond))
	if *show == "" {
		type kv struct {
			k string
			n int
		}
		var cmds []kv
		for c, slots := range e.hist.Values {
			n := 0
			for _, vals := range slots {
				n += len(vals)
			}
			cmds = append(cmds, kv{c, n})
		}
		sort.Slice(cmds, func(i, j int) bool { return cmds[i].n > cmds[j].n })
		for i, c := range cmds {
			if i == 20 {
				break
			}
			fmt.Printf("  %-20s %d values\n", c.k, c.n)
		}
		return nil
	}
	name := e.aliases.Resolve(*show)
	keys := fs.Args()
	if len(keys) == 0 {
		for k := range e.hist.Values[name] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	for _, k := range keys {
		fmt.Printf("%s %s:\n", name, k)
		for i, c := range e.hist.Candidates(name, k, time.Now()) {
			if i == 10 {
				break
			}
			fmt.Printf("  %-40s x%d  %.2f\n", c.Value, c.Count, c.Score)
		}
	}
	return nil
}

func cmdComplete(args []string) error {
	fs := flag.NewFlagSet("complete", flag.ContinueOnError)
	shellName := shellFlag(fs)
	slot := fs.String("slot", "", "slot key to complete (default: last slot; \"-\" for flags)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sh, err := shell.Resolve(*shellName)
	if err != nil {
		return err
	}
	e, err := loadEnv(sh, true)
	if err != nil {
		return err
	}
	line := parse.Parse(strings.Join(fs.Args(), " "))
	if len(line.Segments) == 0 {
		return errors.New("empty command")
	}
	cmd := e.cls.Classify(line, 0)
	sp := e.reg.Lookup(cmd.Name)
	focus := len(cmd.Slots) - 1
	switch *slot {
	case "":
	case "-":
		focus = -1
	default:
		if focus = cmd.Find(*slot); focus < 0 {
			return fmt.Errorf("no slot %q in command", *slot)
		}
	}
	if focus >= 0 {
		req := complete.Synthesize(cmd, focus)
		fmt.Printf("slot %s  argv %q  line %q\n", cmd.Slots[focus].Key, req.Argv, req.Line)
	}
	eng := engine.New(nil, &complete.Cache{Dir: filepath.Join(cacheDir(), "complete")})
	done := make(chan struct{})
	eng.Start(context.Background(), 1, cmd, sp, focus, func(u engine.Update) {
		tag := u.Source
		if u.Stale {
			tag += " (stale)"
		}
		if u.Err != nil {
			fmt.Printf("[%s] error: %v\n", tag, u.Err)
		}
		for _, c := range u.Result.Candidates {
			fmt.Printf("[%s] %s\t%s\n", tag, c.Value, c.Desc)
		}
		if u.Done {
			close(done)
		}
	})
	<-done
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	shellName := shellFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	problems := 0
	check := func(name, value string, err error) {
		status := "ok "
		if err != nil {
			status, value = "ERR", value+": "+err.Error()
			problems++
		}
		fmt.Printf("  [%s] %-11s %s\n", status, name, value)
	}
	fmt.Println("rx:")
	bin, err := os.Executable()
	check("binary", bin, err)

	sh, err := shell.Resolve(*shellName)
	if err != nil {
		check("shell", *shellName, err)
		return fmt.Errorf("%d problem(s)", problems)
	}
	_, err = exec.LookPath(sh.Name)
	check("shell", sh.Name+" on PATH", err)

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		tty.Close()
	}
	check("/dev/tty", "readable and writable", err)

	reg, err := loadRegistry()
	if err == nil {
		var names []string
		for _, s := range reg.Specs() {
			names = append(names, s.Match.Command)
			for _, verr := range s.Validate() {
				err = fmt.Errorf("%s: %v", s.Source, verr)
			}
		}
		check("plugins", strings.Join(names, ", ")+" (user dir "+filepath.Join(configDir(), "plugins")+")", err)
	} else {
		check("plugins", "", err)
	}

	hf := histFile(sh)
	if fi, err := os.Stat(hf); err != nil {
		fmt.Printf("  [-- ] %-11s %s: %v (no history candidates)\n", "history", hf, err)
	} else {
		start := time.Now()
		e, err := loadEnv(sh, true)
		if err == nil && e.hist == nil {
			err = errors.New("could not be indexed")
		}
		check("history", fmt.Sprintf("%s (%d bytes, loaded in %v)", hf, fi.Size(), time.Since(start).Round(time.Millisecond)), err)
	}

	cd := cacheDir()
	err = os.MkdirAll(cd, 0o700)
	if err == nil {
		var f *os.File
		if f, err = os.CreateTemp(cd, "doctor-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
		}
	}
	check("cache", cd, err)

	if problems > 0 {
		return fmt.Errorf("%d problem(s)", problems)
	}
	return nil
}

func cmdPlugins(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rx plugins list|lint [FILE...]")
	}
	switch args[0] {
	case "list":
		reg, err := loadRegistry()
		if err != nil {
			return err
		}
		for _, s := range reg.Specs() {
			fmt.Printf("%-12s %-8s %s\n", s.Match.Command, s.Backend(), s.Source)
		}
		return nil
	case "lint":
		var specs []*spec.Spec
		if len(args) == 1 {
			reg, err := loadRegistry()
			if err != nil {
				return err
			}
			specs = reg.Specs()
		}
		for _, f := range args[1:] {
			data, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			s, err := spec.Parse(data, f)
			if err != nil {
				return err
			}
			specs = append(specs, s)
		}
		bad := 0
		for _, s := range specs {
			for _, err := range s.Validate() {
				fmt.Printf("%s: %v\n", s.Source, err)
				bad++
			}
		}
		if bad > 0 {
			return fmt.Errorf("%d problem(s)", bad)
		}
		fmt.Printf("%d plugin(s) ok\n", len(specs))
		return nil
	}
	return fmt.Errorf("unknown plugins subcommand %q", args[0])
}
