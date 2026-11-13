package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const dockerWindowsHelp = "https://docs.docker.com/desktop/setup/install/windows-install/"
const dockerLinuxHelp = "https://docs.docker.com/engine/install/"
const wslHelp = "https://docs.docker.com/desktop/features/wsl/"
const wslInstallHelp = "https://learn.microsoft.com/en-us/windows/wsl/install"

type Check struct {
	Name, Detail, Help string
	Blocked, Warning   bool
}
type hostPlatform struct{ OS, Arch, Kernel, Distro string }
type commandRunner func(context.Context, string, ...string) (string, error)

func runProbe(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

func currentHost() hostPlatform {
	kernel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return hostPlatform{runtime.GOOS, runtime.GOARCH, string(kernel), os.Getenv("WSL_DISTRO_NAME")}
}

func prerequisites(ctx context.Context, h hostPlatform, run commandRunner) []Check {
	wsl := h.Distro != "" || strings.Contains(strings.ToLower(h.Kernel), "microsoft")
	checks := []Check{{Name: "Host", Detail: h.OS + " / " + h.Arch}}
	if (h.OS != "windows" && h.OS != "linux") || h.Arch != "amd64" {
		checks[0].Blocked = true
		checks[0].Detail += " — use Windows or Linux x86-64 for FXServer"
	}
	dockerHelp, repair := dockerLinuxHelp, "Install Docker Engine and its Compose plugin; start the daemon and grant your account access to its socket. Reopen this terminal after permission changes."
	if h.OS == "windows" || wsl {
		dockerHelp = dockerWindowsHelp
		repair = "Install and start Docker Desktop, wait until its engine is running, and select Linux containers. Reopen the terminal if Docker was just installed."
	}
	if wsl {
		check := Check{Name: "WSL", Detail: "WSL 2 detected. Enable this distribution in Docker Desktop > Settings > Resources > WSL Integration.", Help: wslHelp}
		if !strings.Contains(strings.ToLower(h.Kernel), "microsoft-standard") && !strings.Contains(strings.ToLower(h.Kernel), "wsl2") {
			check.Warning = true
			check.Detail = "WSL detected; verify version 2 with wsl.exe --list --verbose. Convert with wsl --set-version <distro> 2 in PowerShell if necessary."
		}
		checks = append(checks, check)
		repair += " Enable integration for this WSL distribution in Docker Desktop settings."
	} else if h.OS == "windows" {
		_, err := run(ctx, "wsl.exe", "--status")
		checks = append(checks, Check{Name: "WSL backend", Warning: err != nil, Help: wslInstallHelp,
			Detail: map[bool]string{true: "WSL is unavailable. For the WSL backend, run wsl --install in elevated PowerShell, restart if requested, then wsl --update. A working Docker Hyper-V backend is also supported.", false: "WSL responds. Docker's Linux engine below is the final readiness check; WSL alone is not sufficient."}[err != nil]})
	}
	git, err := run(ctx, "git", "--version")
	if err != nil {
		git = "Git missing. Install Git and reopen the terminal. New installs can explicitly choose HTTPS archives; resource updates require Git."
	}
	checks = append(checks, Check{Name: "Git", Detail: git, Warning: err != nil, Help: "https://git-scm.com/downloads"})
	cli, err := run(ctx, "docker", "--version")
	if err != nil {
		return append(checks, Check{Name: "Docker CLI", Detail: repair, Help: dockerHelp, Blocked: true})
	}
	checks = append(checks, Check{Name: "Docker CLI", Detail: cli})
	compose, err := run(ctx, "docker", "compose", "version", "--short")
	if err != nil || strings.HasPrefix(strings.TrimPrefix(compose, "v"), "1.") || compose == "" {
		checks = append(checks, Check{Name: "Compose", Detail: "Install the Docker Compose plugin (v2 or later), or update Docker Desktop. The legacy docker-compose command is not sufficient.", Blocked: true, Help: "https://docs.docker.com/compose/install/"})
	} else {
		checks = append(checks, Check{Name: "Compose", Detail: compose})
	}
	endpoint := os.Getenv("DOCKER_HOST")
	err = nil
	// A selected context takes precedence over DOCKER_HOST, just as in Docker CLI.
	if endpoint == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		endpoint, err = run(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	}
	if err != nil || !(strings.HasPrefix(endpoint, "unix://") || strings.HasPrefix(endpoint, "npipe://")) {
		checks = append(checks, Check{Name: "Docker context", Detail: "Select a local Docker context (docker context use default or desktop-linux) and clear remote DOCKER_HOST overrides. This installer needs local bind mounts; remote TCP/SSH engines are unsupported.", Blocked: true, Help: dockerHelp})
	}
	info, err := run(ctx, "docker", "info", "--format", "{{json .}}")
	var engine struct{ OSType, Architecture, ServerVersion string }
	if err != nil || json.Unmarshal([]byte(info), &engine) != nil {
		checks = append(checks, Check{Name: "Docker engine", Detail: repair, Help: dockerHelp, Blocked: true})
	} else if engine.OSType != "linux" || (engine.Architecture != "x86_64" && engine.Architecture != "amd64") {
		checks = append(checks, Check{Name: "Docker engine", Detail: fmt.Sprintf("Detected %s/%s. Switch to Linux containers on an x86-64 engine.", engine.OSType, engine.Architecture), Help: dockerHelp, Blocked: true})
	} else {
		checks = append(checks, Check{Name: "Docker engine", Detail: engine.ServerVersion + " / linux / " + engine.Architecture})
	}
	return checks
}

func checksReady(checks []Check) bool {
	if len(checks) == 0 {
		return false
	}
	for _, c := range checks {
		if c.Blocked {
			return false
		}
	}
	return true
}

func installationPrerequisites(ctx context.Context, c Config, h hostPlatform, run commandRunner) []Check {
	if c.Docker {
		return prerequisites(ctx, h, run)
	}
	checks := []Check{{Name: "Native Windows", Detail: h.OS + " / " + h.Arch, Blocked: h.OS != "windows" || h.Arch != "amd64", Help: "https://docs.fivem.net/docs/server-manual/setting-up-a-server-vanilla/"}}
	git, e := run(ctx, "git", "--version")
	if e != nil {
		git = "Install Git for Windows and reopen this terminal"
	}
	checks = append(checks, Check{Name: "Git", Detail: git, Blocked: e != nil, Help: "https://git-scm.com/install/windows"})
	// FXServer requires the Microsoft VC++ x64 runtime; do not substitute WSL/Docker checks.
	_, e = run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `if (Test-Path "$env:SystemRoot\System32\vcruntime140.dll") { 'installed' } else { exit 1 }`)
	checks = append(checks, Check{Name: "Visual C++ x64 runtime", Detail: map[bool]string{true: "Installed", false: "Install the Microsoft Visual C++ x64 Redistributable, then retry"}[e == nil], Blocked: e != nil, Help: "https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist"})
	if c.WindowsService || c.DBSetup == "new-service" {
		e = nativePrivilegeCheck()
		detail := "Service registration permissions available"
		if e != nil {
			detail = e.Error()
		}
		checks = append(checks, Check{Name: "Administrator", Detail: detail, Warning: e != nil, Help: "https://learn.microsoft.com/en-us/windows/security/application-security/application-control/user-account-control/"})
	}
	return checks
}
