package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func nativePrivilegeCheck() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("creating Windows services requires Administrator: reopen PowerShell as Administrator and run kora again")
	}
	return nil
}
func serviceAvailable(name string) error {
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	s, e := m.OpenService(name)
	if e == nil {
		s.Close()
		return errors.New("service already exists; existing services are never replaced")
	}
	if !errors.Is(e, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return e
	}
	return nil
}
func createMariaService(ctx context.Context, root string, c Config, name string) error {
	if e := nativePrivilegeCheck(); e != nil {
		return e
	}
	if e := serviceAvailable(name); e != nil {
		return e
	}
	data := filepath.Join(root, "database-data")
	if _, e := os.Lstat(data); !os.IsNotExist(e) {
		return errors.New("database directory already exists or is inaccessible")
	}
	executable := filepath.Join(root, "database", "bin", "mariadb-install-db.exe")
	if !regularFile(executable) {
		executable = filepath.Join(root, "database", "bin", "mysql_install_db.exe")
	}
	template := "[mysqld]\nbind-address=127.0.0.1\n"
	if e := writeNew(root, "database-config.ini", []byte(template), 0600); e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, executable, "--datadir="+data, "--service="+name, "--password="+c.DBAdminPassword, fmt.Sprintf("--port=%d", c.DBPort), "--config="+filepath.Join(root, "database-config.ini"))
	cmd.Dir = filepath.Join(root, "database")
	if e := cmd.Run(); e != nil {
		return errors.New("MariaDB service initialization failed; retained database files and service metadata are in the installation directory")
	}
	return controlNativeService(ctx, name, "start", "")
}
func registerFXService(root string, n nativeInstallation) error {
	if n.Service == "" {
		return nil
	}
	if e := nativePrivilegeCheck(); e != nil {
		return e
	}
	if e := serviceAvailable(n.Service); e != nil {
		return e
	}
	// LocalService can read private files and write runtime state without full system privileges.
	if e := exec.Command("icacls", root, "/grant", "*S-1-5-19:(OI)(CI)M").Run(); e != nil {
		return errors.New("cannot grant LocalService access to the installation")
	}
	manager, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer manager.Disconnect()
	deps := []string{}
	if n.DatabaseService != "" {
		deps = append(deps, n.DatabaseService)
	}
	s, e := manager.CreateService(n.Service, filepath.Join(root, "kora.exe"), mgr.Config{DisplayName: n.Service + " FXServer", Description: "Kora native FXServer: " + root, StartType: mgr.StartAutomatic, ServiceStartName: `NT AUTHORITY\LocalService`, Dependencies: deps}, "--native-service", root)
	if e != nil {
		return e
	}
	defer s.Close()
	return nil
}
func controlNativeService(ctx context.Context, name, action, expectedRoot string) error {
	m, e := mgr.Connect()
	if e != nil {
		return e
	}
	defer m.Disconnect()
	s, e := m.OpenService(name)
	if e != nil {
		return e
	}
	defer s.Close()
	if expectedRoot != "" {
		cfg, e := s.Config()
		if e != nil {
			return e
		}
		expected := windows.ComposeCommandLine([]string{filepath.Join(expectedRoot, "kora.exe"), "--native-service", expectedRoot})
		if cfg.BinaryPathName != expected {
			return errors.New("service executable does not match this installation")
		}
	}
	state, e := s.Query()
	if e != nil {
		return e
	}
	if action == "stop" || action == "restart" {
		if state.State != svc.Stopped {
			if _, e = s.Control(svc.Stop); e != nil {
				return e
			}
			for i := 0; i < 30; i++ {
				state, e = s.Query()
				if e != nil {
					return e
				}
				if state.State == svc.Stopped {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
			if state.State != svc.Stopped {
				return errors.New("service did not stop within 30 seconds")
			}
		}
		if action == "stop" {
			return nil
		}
	}
	if state.State == svc.Running {
		return nil
	}
	return s.Start()
}
func nativeEventName(root string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(root))))
	return fmt.Sprintf(`Global\Kora-%x`, sum[:16])
}
func nativeAction(ctx context.Context, root, action string) error {
	n, e := nativeMeta(root)
	if e != nil {
		return e
	}
	if n.Service != "" {
		return controlNativeService(ctx, n.Service, action, root)
	}
	name, _ := windows.UTF16PtrFromString(nativeEventName(root))
	event, e := windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.SYNCHRONIZE, false, name)
	if e == nil {
		if action == "start" {
			windows.CloseHandle(event)
			return nil
		}
		setErr := windows.SetEvent(event)
		windows.CloseHandle(event)
		if setErr != nil {
			return setErr
		}
		stopped := false
		for i := 0; i < 100; i++ {
			probe, err := windows.OpenEvent(windows.SYNCHRONIZE, false, name)
			if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
				stopped = true
				break
			}
			if err != nil {
				return err
			}
			windows.CloseHandle(probe)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if !stopped {
			return errors.New("native process has not stopped yet")
		}
		if action == "stop" {
			return nil
		}
		e = windows.ERROR_FILE_NOT_FOUND
	}
	if !errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
		return e
	}
	if action == "stop" {
		return nil
	}
	if e = checkPortsMustRead(root); e != nil {
		return e
	}
	cmd := exec.Command(filepath.Join(root, "kora.exe"), "--native-run", root)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
	if e = cmd.Start(); e != nil {
		return e
	}
	return cmd.Process.Release()
}
func checkPortsMustRead(root string) error {
	c, e := serverConfig(root)
	if e != nil {
		return e
	}
	return checkPorts(c)
}
func nativeStatus(root string) string {
	n, e := nativeMeta(root)
	if e != nil {
		return "Native metadata unavailable"
	}
	if n.Service != "" {
		m, e := mgr.Connect()
		if e != nil {
			return "Service unavailable"
		}
		defer m.Disconnect()
		s, e := m.OpenService(n.Service)
		if e != nil {
			return "Service not registered"
		}
		defer s.Close()
		state, e := s.Query()
		if e != nil {
			return "Service status unavailable"
		}
		if state.State == svc.Running {
			return "Native service running"
		}
		if state.State == svc.Stopped {
			return "Native service stopped"
		}
		return "Native service starting or stopping"
	}
	name, _ := windows.UTF16PtrFromString(nativeEventName(root))
	event, e := windows.OpenEvent(windows.SYNCHRONIZE, false, name)
	if e == nil {
		windows.CloseHandle(event)
		return "Native process running"
	}
	return "Native process stopped"
}
func nativeRun(ctx context.Context, root string) (result error) {
	defer func() {
		if result != nil && regularFile(filepath.Join(root, "install.complete")) {
			os.MkdirAll(filepath.Join(root, "runtime"), 0700)
			if f, e := os.OpenFile(filepath.Join(root, "runtime", "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); e == nil {
				fmt.Fprintln(f, "[KORA] Native startup failed:", result)
				f.Close()
			}
		}
	}()
	name, _ := windows.UTF16PtrFromString(nativeEventName(root))
	event, e := windows.CreateEvent(nil, 1, 0, name)
	if errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(event)
		return errors.New("this server is already running")
	}
	if e != nil {
		return e
	}
	defer windows.CloseHandle(event)
	cmd, e := nativeSession(root)
	if e != nil {
		return e
	}
	log, e := os.OpenFile(filepath.Join(root, "runtime", "server.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		return e
	}
	if e = cmd.Start(); e != nil {
		return e
	}
	process, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return e
	}
	e = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return e
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case e := <-done:
			return e
		case <-ctx.Done():
			windows.TerminateJobObject(job, 0)
			<-done
			return nil
		case <-time.After(200 * time.Millisecond):
			result, _ := windows.WaitForSingleObject(event, 0)
			if result == windows.WAIT_OBJECT_0 {
				windows.TerminateJobObject(job, 0)
				<-done
				return nil
			}
		}
	}
}

type nativeServiceHandler struct{ root string }

func (h nativeServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	go func() { done <- nativeRun(ctx, h.root) }()
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	status <- current
	for {
		select {
		case e := <-done:
			if e != nil {
				return true, 1
			}
			return false, 0
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- current
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 10000}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}
func nativeServiceRun(root string) error {
	n, e := nativeMeta(root)
	if e != nil {
		return e
	}
	return svc.Run(n.Service, nativeServiceHandler{root})
}
