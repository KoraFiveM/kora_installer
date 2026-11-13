package main

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestPrerequisites(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	base := map[string]string{
		"git --version":                  "git version 2.48.0",
		"wsl.exe --status":               "ready",
		"docker --version":               "Docker version 28.0.0",
		"docker compose version --short": "2.35.0",
		"docker context inspect --format {{.Endpoints.docker.Host}}": "unix:///var/run/docker.sock",
		"docker info --format {{json .}}":                            `{"OSType":"linux","Architecture":"x86_64","ServerVersion":"28.0.0"}`,
	}
	for _, tc := range []struct {
		name, missing, info string
		host                hostPlatform
		ready               bool
	}{
		{"linux", "", "", hostPlatform{OS: "linux", Arch: "amd64"}, true},
		{"windows hyperv without wsl", "wsl.exe --status", "", hostPlatform{OS: "windows", Arch: "amd64"}, true},
		{"wsl integration missing", "docker --version", "", hostPlatform{OS: "linux", Arch: "amd64", Kernel: "microsoft-standard-WSL2", Distro: "Ubuntu"}, false},
		{"stopped daemon", "docker info --format {{json .}}", "", hostPlatform{OS: "linux", Arch: "amd64"}, false},
		{"missing compose", "docker compose version --short", "", hostPlatform{OS: "windows", Arch: "amd64"}, false},
		{"windows containers", "", `{"OSType":"windows","Architecture":"x86_64"}`, hostPlatform{OS: "windows", Arch: "amd64"}, false},
		{"arm engine", "", `{"OSType":"linux","Architecture":"aarch64"}`, hostPlatform{OS: "linux", Arch: "amd64"}, false},
		{"git optional", "git --version", "", hostPlatform{OS: "linux", Arch: "amd64"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, name string, args ...string) (string, error) {
				key := name + " " + strings.Join(args, " ")
				if key == tc.missing {
					return "", errors.New("missing")
				}
				if key == "docker info --format {{json .}}" && tc.info != "" {
					return tc.info, nil
				}
				value, ok := base[key]
				if !ok {
					t.Fatalf("unexpected probe: %s", key)
				}
				return value, nil
			}
			checks := prerequisites(context.Background(), tc.host, run)
			if checksReady(checks) != tc.ready {
				t.Fatalf("unexpected readiness: %+v", checks)
			}
			for _, c := range checks {
				if c.Blocked && (c.Detail == "" || c.Help == "") {
					t.Fatalf("missing repair guidance: %+v", c)
				}
			}
		})
	}
	t.Setenv("DOCKER_HOST", "ssh://remote.example")
	checks := prerequisites(context.Background(), hostPlatform{OS: "linux", Arch: "amd64"}, func(_ context.Context, name string, args ...string) (string, error) {
		return base[name+" "+strings.Join(args, " ")], nil
	})
	if checksReady(checks) {
		t.Fatal("accepted remote Docker engine with local bind mounts")
	}
}

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func TestStageUpdatePreservesLiveTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	for _, mode := range []string{"clean", "dirty", "diverged", "changed-since-review"} {
		t.Run(mode, func(t *testing.T) {
			upstream := t.TempDir()
			gitTest(t, upstream, "init")
			gitTest(t, upstream, "config", "user.email", "fixture@example.invalid")
			gitTest(t, upstream, "config", "user.name", "Fixture")
			os.WriteFile(filepath.Join(upstream, "resource.lua"), []byte("old"), 0600)
			gitTest(t, upstream, "add", ".")
			gitTest(t, upstream, "commit", "-m", "initial")
			old := gitTest(t, upstream, "rev-parse", "HEAD")
			server := t.TempDir()
			source := filepath.Join(server, "resources", "[kora]", "fixture")
			os.MkdirAll(filepath.Dir(source), 0700)
			gitTest(t, server, "clone", "--", upstream, source)
			os.WriteFile(filepath.Join(upstream, "resource.lua"), []byte("new"), 0600)
			gitTest(t, upstream, "commit", "-am", "update")
			target := gitTest(t, upstream, "rev-parse", "HEAD")
			if mode == "dirty" {
				os.WriteFile(filepath.Join(source, "local.lua"), []byte("keep"), 0600)
			}
			if mode == "diverged" || mode == "changed-since-review" {
				gitTest(t, source, "config", "user.email", "fixture@example.invalid")
				gitTest(t, source, "config", "user.name", "Fixture")
				os.WriteFile(filepath.Join(source, "local.lua"), []byte("keep"), 0600)
				gitTest(t, source, "add", ".")
				gitTest(t, source, "commit", "-m", "local")
				if mode == "diverged" {
					old = gitTest(t, source, "rev-parse", "HEAD")
				}
			}
			before := gitTest(t, source, "rev-parse", "HEAD")
			p := UpdatePlan{Server: Server{Path: server}, Resources: []Checkout{{Resource: Resource{Path: "[kora]/fixture", Repository: upstream, Ref: "default", Commit: target}, Current: old}}}
			result := stageUpdate(context.Background(), p, func(Progress) {})
			if (result.Err == nil) != (mode == "clean") {
				t.Fatalf("unexpected staging outcome: %v", result.Err)
			}
			if got := gitTest(t, source, "rev-parse", "HEAD"); got != before {
				t.Fatal("modified live checkout")
			}
			content, _ := os.ReadFile(filepath.Join(source, "resource.lua"))
			if string(content) != "old" {
				t.Fatal("overwrote live resource")
			}
			if mode == "clean" {
				if !regularFile(filepath.Join(result.Folder, "update.complete")) {
					t.Fatal("missing completion marker")
				}
				if got := gitTest(t, filepath.Join(result.Folder, "resources", "[kora]", "fixture"), "rev-parse", "HEAD"); got != target {
					t.Fatal("wrong staged commit")
				}
			} else if regularFile(filepath.Join(result.Folder, "update.complete")) {
				t.Fatal("marked partial update complete")
			}
			if _, err := os.Stat(filepath.Join(server, "database-data")); !os.IsNotExist(err) {
				t.Fatal("update touched database storage")
			}
		})
	}
}

func TestTemplateExportIsReusableAndExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.json")
	if err := exportTemplate(path, nil, nil); err != nil {
		t.Fatal(err)
	}
	r, err := loadRecipe(path)
	if err != nil {
		t.Fatal(err)
	}
	list, err := resources(r)
	if err != nil || len(list) < 10 {
		t.Fatalf("unusable base template: %v", err)
	}
	if err := exportTemplate(path, nil, nil); err == nil {
		t.Fatal("overwrote template")
	}
}

func TestServerDiscoveryRecognizesPartialAndLegacy(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	parent := t.TempDir()
	for _, name := range []string{"complete", "partial", "legacy", "unrelated"} {
		root := filepath.Join(parent, name)
		os.MkdirAll(filepath.Join(root, "server-data"), 0700)
		os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}"), 0600)
		switch name {
		case "complete", "partial":
			os.WriteFile(filepath.Join(root, "start.py"), nil, 0600)
			os.WriteFile(filepath.Join(root, "server-data", "server.cfg"), nil, 0600)
			if name == "complete" {
				os.WriteFile(filepath.Join(root, "install.complete"), nil, 0600)
			}
		case "legacy":
			os.WriteFile(filepath.Join(root, "resources.manifest"), nil, 0600)
		}
	}
	// No real Docker commands or server reads outside this temporary working directory.
	t.Chdir(parent)
	list := discoverServersWith(context.Background(), Config{Output: parent}, func(context.Context, string, ...string) (string, error) { return "[]", nil })
	if len(list) != 3 {
		t.Fatalf("unexpected servers: %+v", list)
	}
	for _, server := range list {
		if filepath.Base(server.Path) == "partial" && !strings.Contains(server.Status, "Incomplete") {
			t.Fatal("partial installation marked ready")
		}
	}
}

func TestDatabasePublishPortValidation(t *testing.T) {
	c := validConfig(t)
	c.DBExposePort = true
	c.DBHostPort = c.CFXPort
	if c.Validate() == nil {
		t.Fatal("accepted database/game port collision")
	}
	c.DBHostPort = 3306
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"resources/secret.json", "database-data/secret.json", "installation.json", "base-template.json"} {
		c.Secrets = true
		c.SecretsPath = path
		if c.Validate() == nil {
			t.Fatalf("accepted reserved export: %s", path)
		}
	}
}

func TestInstallerLaunchPreservesSecretsWithoutHostPython(t *testing.T) {
	root := t.TempDir()
	c := validConfig(t)
	c.DBPassword = "p$word'with spaces"
	if err := generate(root, Plan{Config: c}); err != nil {
		t.Fatal(err)
	}
	if _, err := dockerCommand(context.Background(), root, "up"); err == nil {
		t.Fatal("launched incomplete installation")
	}
	os.WriteFile(filepath.Join(root, "install.complete"), []byte("complete"), 0600)
	t.Setenv("COMPOSE_PROJECT_NAME", "unrelated-server")
	cmd, err := dockerCommand(context.Background(), root, "up", "--build")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, env := range cmd.Env {
		if env == "DB_PASSWORD="+c.DBPassword {
			found = true
		}
		if strings.HasPrefix(env, "COMPOSE_") {
			t.Fatal("ambient Compose override leaked")
		}
	}
	if !found {
		t.Fatal("password was lost or interpolated")
	}
	if strings.Contains(strings.Join(cmd.Args, " "), c.DBPassword) {
		t.Fatal("secret placed in argv")
	}
}

func TestPasteAndPreflightGate(t *testing.T) {
	m := newModel(validConfig(t), map[string]bool{})
	for i, f := range fields {
		if f.Key == "license-key" {
			m.step = i
		}
	}
	m.loadField()
	m.input.SetValue("")
	next, _ := m.Update(tea.PasteMsg{Content: "pasted-license"})
	m = next.(model)
	if m.input.Value() != "pasted-license" {
		t.Fatal("paste dropped")
	}
	if strings.Contains(m.View().Content, "pasted-license") {
		t.Fatal("paste exposed secret")
	}
	m.stage = "preflight"
	m.checks = []Check{{Name: "Docker", Blocked: true}}
	m.afterChecks = "new"
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.stage != "preflight" {
		t.Fatal("bypassed blocked prerequisites")
	}
	m.checks[0].Blocked = false
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.stage != "form" {
		t.Fatal("could not continue after prerequisites repaired")
	}
}

func TestManagerFitsNarrowTerminal(t *testing.T) {
	m := newModel(validConfig(t), map[string]bool{})
	m.servers = []Server{{Path: "/a/long/path/to/a/development/server", Status: "Complete", Managed: true}}
	m.checks = []Check{{Name: "Docker", Detail: "Install and start Docker Desktop, then enable your WSL distribution.", Help: dockerWindowsHelp, Blocked: true}}
	for _, width := range []int{40, 80, 120} {
		m.width, m.height = width, 30
		for _, stage := range []string{"welcome", "preflight", "servers", "server", "template", "start-choice", "form"} {
			m.stage = stage
			if got := lipgloss.Width(m.View().Content); got > width {
				t.Fatalf("%s renders %d columns in %d-column terminal", stage, got, width)
			}
		}
	}
}

func TestAdminerConfiguration(t *testing.T) {
	c, seen, err := flags([]string{"--adminer=true", "--adminer-port=18080"}, io.Discard)
	if err != nil || !c.Adminer || c.AdminerPort != 18080 || !seen["adminer"] {
		t.Fatalf("Adminer flags lost: %v", err)
	}
	c = validConfig(t)
	c.Adminer = true
	for _, port := range []int{c.CFXPort, c.TXPort, c.DBHostPort} {
		c.TXAdmin = true
		c.DBExposePort = true
		c.AdminerPort = port
		if c.Validate() == nil {
			t.Fatalf("accepted Adminer collision on %d", port)
		}
	}
	c.AdminerPort = 18080
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := generate(root, Plan{Config: c}); err != nil {
		t.Fatal(err)
	}
	loaded, err := serverConfig(root)
	if err != nil || !loaded.Adminer || loaded.AdminerPort != 18080 {
		t.Fatalf("Adminer launch settings lost: %v", err)
	}
	link, err := url.Parse(adminerURL(loaded))
	if err != nil {
		t.Fatal(err)
	}
	q := link.Query()
	if q.Get("server") != "db" || q.Get("db") != c.DBName || q.Get("username") != c.DBUser || q.Has("password") || strings.Contains(link.String(), c.DBPassword) {
		t.Fatal("incorrect Adminer login link")
	}
	// Existing installations from before this option remain startable.
	old := t.TempDir()
	os.WriteFile(filepath.Join(old, ".env"), []byte("CFX_PORT=\"30120\"\nTXADMIN_PORT=\"40120\"\n"), 0600)
	loaded, err = serverConfig(old)
	if err != nil || loaded.Adminer {
		t.Fatalf("old installation requires Adminer fields: %v", err)
	}
}

func TestServerActionsKeepSelectedIdentity(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "install.complete"), nil, 0600)
	m := newModel(validConfig(t), map[string]bool{})
	m.servers = []Server{{Path: root, Managed: true}, {Path: "another-server", Managed: true}}
	m.stage = "server"
	next, _, handled := m.updateDashboard(tea.KeyPressMsg{Code: 'x'})
	if !handled || next.stage != "server-action" || next.actionRoot != root || next.serverAction != "stop" {
		t.Fatal("stop confirmation did not capture server identity")
	}
	next.servers = []Server{{Path: "another-server", Managed: true}}
	if next.actionRoot != root {
		t.Fatal("refresh retargeted operation")
	}
	next, _, _ = next.updateDashboard(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.stage != "server" {
		t.Fatal("confirmation cannot be cancelled")
	}
}
