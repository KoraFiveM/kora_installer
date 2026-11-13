//go:build !windows || !gui

package main

import "os/exec"

func launchGUI(Config) (bool, int) { return false, 0 }

func backgroundCommand(cmd *exec.Cmd) *exec.Cmd { return cmd }
