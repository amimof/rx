package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/amimof/rx/internal/config"
	"github.com/amimof/rx/internal/shell"
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [--shell SHELL]",
		Short: "Check the setup (run rx-doctor in the shell for key bindings too)",
		Args:  cobra.NoArgs,
	}
	shellName := addShellFlag(cmd)
	cmd.RunE = func(*cobra.Command, []string) error { return runDoctor(*shellName) }
	return cmd
}

func runDoctor(shellName string) error {
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

	sh, err := shell.Resolve(shellName)
	if err != nil {
		check("shell", shellName, err)
		return fmt.Errorf("%d problem(s)", problems)
	}
	_, err = exec.LookPath(sh.Name)
	check("shell", sh.Name+" on PATH", err)

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		tty.Close()
	}
	check("/dev/tty", "readable and writable", err)

	cfg, err := config.Load(cfgFile)
	switch {
	case err != nil:
		check("config", "", err)
	case cfg.File == "":
		check("config", "defaults ("+config.Path()+" not found)", nil)
	default:
		check("config", cfg.File, nil)
	}
	if err == nil {
		check("complete", cfg.Complete.String(), nil)
		check("rules", cfg.Rules, nil)
		check("legend", strconv.FormatBool(cfg.Legend), nil)
		height := cfg.Height.String()
		if height == "" {
			height = "auto"
		}
		check("height", height, nil)
	}

	reg, err := loadRegistry()
	if err == nil {
		var names []string
		for _, s := range reg.Specs() {
			names = append(names, s.Match.Command)
			for _, verr := range s.Validate() {
				err = fmt.Errorf("%s: %v", s.Source, verr)
			}
		}
		check("plugins", strings.Join(names, ", ")+" (user dir "+filepath.Join(config.Dir(), "plugins")+")", err)
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
