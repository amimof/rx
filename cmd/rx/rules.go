package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/parse"
	"github.com/amimof/rx/internal/rule"
	"github.com/amimof/rx/internal/shell"
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules [--shell SHELL] -- CMD",
		Short: "Print the rules for a command (debugging)",
		Long: `Print each rule the command's plugin defines, whether its conditions hold
for CMD or which one doesn't, and the command its actions would produce.`,
	}
	shellName := addShellFlag(cmd)
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return runRules(*shellName, strings.Join(args, " "))
	}
	return cmd
}

func runRules(shellName, buf string) error {
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
	if sp == nil || len(sp.Rules) == 0 {
		return fmt.Errorf("no rules for %s", cmd.Name)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for i := range sp.Rules {
		r := &sp.Rules[i]
		status := "holds"
		if ok, why := rule.Holds(cmd, sp, r); !ok {
			status = why
		}
		out := cmd.Clone()
		rule.Fire(out, sp, r)
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Name, status, out.Render())
	}
	return w.Flush()
}
