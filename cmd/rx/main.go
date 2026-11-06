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
	"unicode/utf8"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/complete/zsh"
	"github.com/amimof/rx/internal/engine"
	"github.com/amimof/rx/internal/history"
	"github.com/amimof/rx/internal/parse"
	zshinit "github.com/amimof/rx/internal/shell/zsh"
	"github.com/amimof/rx/internal/spec"
	"github.com/amimof/rx/internal/tui"
	"github.com/amimof/rx/plugins"
)

const usage = `rx - edit shell commands as slots

Usage:
  rx init zsh                       print the zsh widget (eval "$(rx init zsh)")
  rx edit [--cursor N] -- BUFFER    open the slot editor (normally run by the widget)
  rx index [--show CMD [SLOT]]      build the history index and show candidates
  rx complete [--slot KEY] -- CMD   print completions for a slot (debugging)
  rx plugins list                   list loaded plugins
  rx plugins lint [FILE...]         validate plugin files
  rx doctor                         check the setup (in zsh, run rx-doctor for
                                    key bindings too)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
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
		err = cmdDoctor()
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if errors.Is(err, errCancelled) {
		os.Exit(1)
	}
	if err != nil {
		// Under the widget stderr is the log file, so stamp the line.
		if os.Getenv("RX_COPROC") == "1" {
			fmt.Fprint(os.Stderr, time.Now().Format(time.RFC3339), " ")
		}
		fmt.Fprintln(os.Stderr, "rx:", err)
		os.Exit(1)
	}
}

var errCancelled = errors.New("cancelled")

func cmdInit(args []string) error {
	if len(args) != 1 || args[0] != "zsh" {
		return errors.New("usage: rx init zsh")
	}
	bin, err := os.Executable()
	if err != nil {
		bin = "rx"
	}
	fmt.Print(zshinit.Script(bin))
	return nil
}

// env holds everything loaded at startup.
type env struct {
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

func histFile() string {
	for _, v := range []string{"RX_HISTFILE", "HISTFILE"} {
		if f := os.Getenv(v); f != "" {
			return f
		}
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".zsh_history")
}

// parseAliases decodes RX_ALIASES: escaped "name=value" lines.
func parseAliases(s string) spec.Aliases {
	a := spec.Aliases{}
	for _, l := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(complete.Unescape(l), "="); ok && k != "" {
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

func loadEnv(withHistory bool) (*env, error) {
	reg, err := loadRegistry()
	if err != nil {
		return nil, err
	}
	e := &env{reg: reg, aliases: parseAliases(os.Getenv("RX_ALIASES"))}
	e.cls = &spec.Classifier{Specs: reg, Aliases: e.aliases}
	if withHistory {
		x, err := history.Load(histFile(), cacheDir(), e.salt(), *e.cls)
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

// salt changes whenever specs or aliases change, invalidating the history
// index, since both affect how history lines are classified.
func (e *env) salt() string {
	data, _ := json.Marshal(struct {
		S []*spec.Spec
		A spec.Aliases
	}{e.reg.Specs(), e.aliases})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func runeToByte(s string, runes int) int {
	b := 0
	for i := 0; i < runes && b < len(s); i++ {
		_, n := utf8.DecodeRuneInString(s[b:])
		b += n
	}
	return b
}

func cmdEdit(args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	cursor := fs.Int("cursor", -1, "cursor position in characters (default: end)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	buf := strings.Join(fs.Args(), " ")

	var client *zsh.Client
	if os.Getenv("RX_COPROC") == "1" {
		client = zsh.NewClient(os.Stdin, os.Stdout)
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

	pos := len(buf)
	if *cursor >= 0 {
		pos = runeToByte(buf, *cursor)
	}
	line := parse.Parse(buf)
	seg := line.SegmentAt(pos)
	if seg < 0 {
		return fail(errors.New("nothing to edit"))
	}

	e, err := loadEnv(true)
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

	eng := engine.New(client, &complete.Cache{Dir: filepath.Join(cacheDir(), "complete")})
	out, err := tui.Run(tui.Options{Cmd: cmd, Spec: sp, History: e.hist, Engine: eng, Infer: e.hist, TTY: tty})
	if err != nil {
		return fail(fmt.Errorf("editor: %w", err))
	}
	if out.Cancelled {
		return cancel()
	}
	rendered := out.Cmd.Render()
	result := line.Splice(seg, rendered)
	end := utf8.RuneCountInString(result[:line.Segments[seg].Start+len(rendered)])
	if client != nil {
		return client.Done(result, end)
	}
	fmt.Println(result)
	return nil
}

func cmdIndex(args []string) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	show := fs.String("show", "", "command to show candidates for")
	if err := fs.Parse(args); err != nil {
		return err
	}
	start := time.Now()
	e, err := loadEnv(true)
	if err != nil {
		return err
	}
	if e.hist == nil {
		return fmt.Errorf("no history at %s", histFile())
	}
	fmt.Printf("indexed %s (%d bytes) in %v\n", histFile(), e.hist.Size, time.Since(start).Round(time.Millisecond))
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
	slot := fs.String("slot", "", "slot key to complete (default: last slot; \"-\" for flags)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	e, err := loadEnv(true)
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

func cmdDoctor() error {
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

	_, err = exec.LookPath("zsh")
	check("zsh", "on PATH", err)

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

	hf := histFile()
	if fi, err := os.Stat(hf); err != nil {
		fmt.Printf("  [-- ] %-11s %s: %v (no history candidates)\n", "history", hf, err)
	} else {
		start := time.Now()
		e, err := loadEnv(true)
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
