//go:build !windows || !gui

package main

func launchGUI(Config) (bool, int) { return false, 0 }
