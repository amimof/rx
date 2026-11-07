package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/complete"
	"github.com/amimof/rx/internal/engine"
	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/shell"
)

func newCompleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "complete [--shell SHELL] [--slot KEY] -- CMD",
		Short: "Print completions for a slot (debugging)",
	}
	shellName := addShellFlag(cmd)
	slot := cmd.Flags().String("slot", "", `slot key to complete (default: last slot; "-" for flags)`)
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return runComplete(*shellName, *slot, strings.Join(args, " "))
	}
	return cmd
}

func runComplete(shellName, slot, buf string) error {
	sh, err := shell.Resolve(shellName)
	if err != nil {
		return err
	}
	e, err := loadEnv(sh, true)
	if err != nil {
		return err
	}
	line := parse.Parse(buf)
	if len(line.Segments) == 0 {
		return errors.New("empty command")
	}
	cmd := e.cls.Classify(line, 0)
	sp := e.reg.Lookup(cmd.Name)
	focus := len(cmd.Slots) - 1
	switch slot {
	case "":
	case "-":
		focus = -1
	default:
		if focus = cmd.Find(slot); focus < 0 {
			return fmt.Errorf("no slot %q in command", slot)
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
