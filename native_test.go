package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeTestConfig(t *testing.T) Config {
	c := validConfig(t)
	c.Docker = false
	c.DBSetup = "existing"
	return c
}
func TestNativeLayoutAndSessions(t *testing.T) {
	c := nativeTestConfig(t)
	c.TXAdmin = true
	root := t.TempDir()
	if e := generate(root, Plan{Config: c}); e != nil {
		t.Fatal(e)
	}
	if regularFile(filepath.Join(root, "compose.yaml")) {
		t.Fatal("native install generated Compose")
	}
	if _, e := os.Stat(nativeResources(root, c)); e != nil {
		t.Fatal(e)
	}
	writeNew(root, "install.complete", []byte("complete"), 0600)
	first, e := nativeSession(root)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(first.Path, "FXServer.exe") || first.Dir != filepath.Join(root, "server-data") {
		t.Fatal(first)
	}
	if strings.Contains(strings.Join(first.Args, " "), c.License) || strings.Contains(strings.Join(first.Args, " "), c.DBPassword) {
		t.Fatal("secret in argv")
	}
	entries, _ := os.ReadDir(filepath.Join(root, "runtime"))
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	original := filepath.Join(root, "runtime", entries[0].Name())
	profile := filepath.Join(original, "txData", "default")
	os.WriteFile(filepath.Join(profile, "admins.json"), []byte("retained-account"), 0600)
	configBefore, _ := os.ReadFile(filepath.Join(profile, "config.json"))
	if _, e = nativeSession(root); e != nil {
		t.Fatal(e)
	}
	configAfter, _ := os.ReadFile(filepath.Join(profile, "config.json"))
	if string(configBefore) != string(configAfter) {
		t.Fatal("previous txAdmin config changed")
	}
	entries, _ = os.ReadDir(filepath.Join(root, "runtime"))
	if len(entries) != 2 {
		t.Fatal(entries)
	}
	copied := false
	for _, entry := range entries {
		if entry.Name() == filepath.Base(original) {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(root, "runtime", entry.Name(), "txData", "default", "admins.json"))
		copied = string(data) == "retained-account"
	}
	if !copied {
		t.Fatal("txAdmin account not retained")
	}
}
func TestNativeMetadataAndDiscovery(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	parent := t.TempDir()
	t.Chdir(parent)
	root := filepath.Join(parent, "native")
	os.Mkdir(root, 0700)
	c := nativeTestConfig(t)
	if e := generate(root, Plan{Config: c}); e != nil {
		t.Fatal(e)
	}
	writeNew(root, "native.json", jsonBytes(nativeInstallation{Version: 1}), 0600)
	writeNew(root, "install.complete", nil, 0600)
	servers := discoverServersWith(context.Background(), Config{Output: parent}, func(context.Context, string, ...string) (string, error) { return "[]", nil })
	if len(servers) != 1 || !servers[0].Managed {
		t.Fatal(servers)
	}
	got, gotErr := os.Stat(resourceDirectory(servers[0]))
	want, wantErr := os.Stat(filepath.Join(root, "server-data", "resources"))
	if gotErr != nil || wantErr != nil || !os.SameFile(got, want) {
		t.Fatal("wrong native resource directory")
	}
}
func TestMariaChecksum(t *testing.T) {
	file := filepath.Join(t.TempDir(), "archive.zip")
	os.WriteFile(file, []byte("archive"), 0600)
	hash := sha256.Sum256([]byte("archive"))
	sum := fmt.Sprintf("%x  ./archive.zip\n", hash)
	if e := verifyChecksum(file, []byte(sum), "archive.zip"); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"", strings.Repeat("0", 64) + "  archive.zip", sum} {
		name := "archive.zip"
		if bad == sum {
			name = "different.zip"
		}
		if e := verifyChecksum(file, []byte(bad), name); e == nil {
			t.Fatal("accepted invalid checksum")
		}
	}
}
func TestNativeWizardAndValidation(t *testing.T) {
	c := nativeTestConfig(t)
	c.DBSetup = "new-service"
	m := newModel(c, map[string]bool{})
	for _, f := range fields {
		switch f.Key {
		case "windows-service", "db-setup", "db-port":
			if m.hidden(f) {
				t.Fatal("native field hidden", f.Key)
			}
		case "adminer", "db-expose-port", "db-admin-password":
			if !m.hidden(f) {
				t.Fatal("irrelevant field shown", f.Key)
			}
		}
	}
	c.DBPort = c.CFXPort
	if c.Validate() == nil {
		t.Fatal("accepted conflicting database port")
	}
	if e := artifactURL("https://runtime.fivem.net/artifacts/fivem/build_server_windows/master/123-abc/server.7z", "windows"); e != nil {
		t.Fatal(e)
	}
}
func TestNativePrerequisitesAvoidDocker(t *testing.T) {
	c := nativeTestConfig(t)
	checks := installationPrerequisites(context.Background(), c, hostPlatform{OS: "windows", Arch: "amd64"}, func(_ context.Context, name string, args ...string) (string, error) {
		if name != "git" && name != "powershell.exe" {
			t.Fatalf("unexpected prerequisite %s", name)
		}
		return "installed", nil
	})
	if !checksReady(checks) {
		t.Fatal(checks)
	}
}
