package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/term"
	"os"
	"time"
)

func run() int {
	c, seen, e := flags(os.Args[1:], os.Stderr)
	if errors.Is(e, flag.ErrHelp) {
		return 0
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "Invalid installer flags:", e, "(run --help)")
		return 2
	}
	if c.Yes {
		return runUnattended(c)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "kora requires an interactive terminal. Flags prefill the TUI; review and confirmation remain interactive.")
		return 2
	}
	final, e := tea.NewProgram(newModel(c, seen)).Run()
	if e != nil {
		fmt.Fprintln(os.Stderr, "Unable to run the installer terminal UI.")
		return 1
	}
	m := final.(model)
	if m.result.Folder != "" {
		fmt.Println("Installation directory:", m.result.Folder)
		if m.result.Err == nil && m.config.Adminer {
			fmt.Println("Adminer (after startup):", adminerURL(m.config))
		}
	}
	if m.cancelled {
		return 130
	}
	if m.result.Err != nil {
		fmt.Fprintln(os.Stderr, "Installation failed:", m.result.Err)
		return 1
	}
	if m.stage == "startup-done" {
		c, err := serverConfig(m.result.Folder)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Cannot read server configuration:", err)
			return 1
		}
		fmt.Print(startupSummary(m.result.Folder, c, m.err, m.history))
		if m.err != "" {
			return 1
		}
	}

	return 0
}
func main() { os.Exit(run()) }

func runUnattended(c Config) int {
	ctx := context.Background()
	if c.Server != "" || c.WriteTemplate != "" {
		fmt.Fprintln(os.Stderr, "--yes is for new installations only")
		return 2
	}
	if err := c.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	checks := prerequisites(ctx, currentHost(), runProbe)
	for _, check := range checks {
		fmt.Println(check.Name + ": " + check.Detail)
		if check.Blocked {
			fmt.Println(check.Help)
		}
	}
	if !checksReady(checks) {
		return 1
	}
	if err := checkPorts(c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	progress := func(p Progress) { fmt.Println(p.Text) }
	plan, err := prepare(ctx, c, progress)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	result := install(ctx, plan, progress)
	fmt.Println("Installation directory:", result.Folder)
	if result.Err != nil {
		fmt.Fprintln(os.Stderr, result.Err)
		return 1
	}
	if c.Start {
		if err := checkPorts(c); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var recent []string
		startupProgress := func(p Progress) {
			progress(p)
			recent = append(recent, p.Text)
			if len(recent) > 16 {
				recent = recent[len(recent)-16:]
			}
		}
		startCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		if err := startup(startCtx, result.Folder, startupProgress); err != nil {
			fmt.Fprint(os.Stderr, startupSummary(result.Folder, c, err.Error(), recent))
			return 1
		}
		fmt.Print(startupSummary(result.Folder, c, "", nil))
	}
	return 0
}
