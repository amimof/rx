package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/config"
	"github.com/amimof/rx/internal/shell"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init SHELL",
		Short: `Print the shell widget (eval "$(rx init zsh)", eval "$(rx init bash)" or rx init fish | source)`,
		Long: `Print the shell widget. The key binding and log file come from the config
file; RX_KEY and RX_LOG set in the shell override them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: rx init %s", strings.Join(shell.Names(), "|"))
			}
			sh, ok := shell.Lookup(args[0])
			if !ok {
				return fmt.Errorf("unsupported shell %q (supported: %s)", args[0], strings.Join(shell.Names(), ", "))
			}
			c, err := config.Load(cfgFile)
			if err != nil {
				return err
			}
			bin, err := os.Executable()
			if err != nil {
				bin = "rx"
			}
			fmt.Print(sh.Script(shell.ScriptOptions{Bin: bin, Key: c.Key, Log: c.Log}))
			return nil
		},
	}
}
