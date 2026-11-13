package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
)

func secureDir(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid := user.User.Sid.String()
	// Protect before writing secrets. Children inherit only this account and SYSTEM.
	cmd := backgroundCommand(exec.Command("icacls", path, "/inheritance:r", "/grant:r", "*"+sid+":(OI)(CI)F", "*S-1-5-18:(OI)(CI)F"))
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("cannot protect installation directory ACL: %w", err)
	}
	return nil
}
