package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/shell"
)

func newIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index [--shell SHELL] [--show CMD [SLOT...]]",
		Short: "Build the history index and show candidates",
	}
	shellName := addShellFlag(cmd)
	show := cmd.Flags().String("show", "", "command to show candidates for")
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return runIndex(*shellName, *show, args)
	}
	return cmd
}

func runIndex(shellName, show string, keys []string) error {
	sh, err := shell.Resolve(shellName)
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
	if show == "" {
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
	name := e.cls.Resolve(show)
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
