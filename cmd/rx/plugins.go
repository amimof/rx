package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/spec"
)

func newPluginsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "List or validate plugins",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List loaded plugins",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			reg, err := loadRegistry()
			if err != nil {
				return err
			}
			for _, s := range reg.Specs() {
				fmt.Printf("%-12s %-8s %s\n", s.Match.Command, s.Backend(), s.Source)
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "lint [FILE...]",
		Short: "Validate plugin files (default: all loaded plugins)",
		RunE:  func(_ *cobra.Command, args []string) error { return runLint(args) },
	})
	return cmd
}

func runLint(files []string) error {
	var specs []*spec.Spec
	if len(files) == 0 {
		reg, err := loadRegistry()
		if err != nil {
			return err
		}
		specs = reg.Specs()
	}
	for _, f := range files {
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
