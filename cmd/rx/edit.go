package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/config"
	"github.com/amimof/rx/internal/engine"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/shell"
	"github.com/amimof/rx/internal/shell/proto"
	"github.com/amimof/rx/internal/tui"
)

func newEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit [--shell SHELL] [--cursor N] -- BUFFER",
		Short: "Open the slot editor",
		Long: `Open the slot editor on BUFFER and print the result. --shell is set by the
widget and makes rx talk to it on stdin/stdout.`,
	}
	shellName := addShellFlag(cmd)
	cursor := cmd.Flags().Int("cursor", -1, "cursor position in the shell's unit; -1 for the end")
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return runEdit(*shellName, *cursor, strings.Join(args, " "))
	}
	return cmd
}

func runEdit(shellName string, cursor int, buf string) error {
	// With --shell, rx was started by the widget and talks to it on stdin
	// and stdout.
	var client *proto.Client
	if shellName != "" {
		widget = true
		client = proto.NewClient(shellName, os.Stdin, os.Stdout)
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
	sh, err := shell.Resolve(shellName)
	if err != nil {
		return fail(err)
	}
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fail(err)
	}

	pos := len(buf)
	if cursor >= 0 {
		pos = sh.ToByte(buf, cursor)
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
	out, err := tui.Run(tui.Options{
		Cmd: cmd, Spec: sp, History: e.hist, Engine: eng, Infer: e.hist, TTY: tty,
		Keys: cfg.Keys, Prompt: cfg.Prompt, Theme: cfg.Theme,
	})
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
		if out.Run {
			return client.Run(result, end)
		}
		return client.Done(result, end)
	}
	// Standalone, the command is printed either way: running it here would
	// bypass the shell's aliases and functions.
	fmt.Println(result)
	return nil
}
