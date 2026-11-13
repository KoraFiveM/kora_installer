//go:build !windows

package main

import (
	"context"
	"errors"
)

func nativePrivilegeCheck() error                                      { return errors.New("native installation requires Windows") }
func registerFXService(string, nativeInstallation) error               { return nativePrivilegeCheck() }
func createMariaService(context.Context, string, Config, string) error { return nativePrivilegeCheck() }
func nativeAction(context.Context, string, string) error               { return nativePrivilegeCheck() }
func nativeRun(context.Context, string) error                          { return nativePrivilegeCheck() }
func nativeServiceRun(string) error                                    { return nativePrivilegeCheck() }
func nativeStatus(string) string                                       { return "Native Windows server (manage on Windows)" }
