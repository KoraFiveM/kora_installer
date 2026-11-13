package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFiveMReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		ready      bool
	}{
		{"starting", "please wait", 503, false},
		{"html", "<html>txAdmin</html>", 200, false},
		{"unrelated JSON", `{}`, 200, false},
		{"ready", `{"resources":["kora_core"],"vars":{"sv_projectName":"Test"}}`, 200, true},
		{"oversized", strings.Repeat(" ", 1024*1024) + `{"resources":[],"vars":{}}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			if got := fiveMReady(context.Background(), server.Client(), server.URL); got != tc.ready {
				t.Fatalf("ready=%v, want %v", got, tc.ready)
			}
		})
	}
}

func TestStartupContainerStates(t *testing.T) {
	for _, tc := range []struct {
		data            string
		running, failed bool
	}{
		{`[{"Service":"fivem","State":"running"}]`, true, false},
		{"{\"Service\":\"db\",\"State\":\"running\"}\n{\"Service\":\"fivem\",\"State\":\"running\"}", true, false},
		{`{"Service":"fivem","State":"exited","ExitCode":1}`, false, true},
		{`[{"Service":"db","State":"running","Health":"unhealthy"}]`, false, true},
		{`[{"Service":"fivem","State":"restarting"}]`, false, false},
		{`invalid`, false, true},
	} {
		running, err := startupContainerState([]byte(tc.data))
		if running != tc.running || (err != nil) != tc.failed {
			t.Fatalf("%s: running=%v err=%v", tc.data, running, err)
		}
	}
}

func TestStartupCompletionAndSummary(t *testing.T) {
	for _, failure := range []error{nil, fmt.Errorf("fivem exited with code 1")} {
		m := newModel(validConfig(t), map[string]bool{})
		m.stage = "starting"
		next, _ := m.Update(startupMsg{failure})
		m = next.(model)
		if m.stage != "startup-done" || m.startupReady != (failure == nil) {
			t.Fatal("incorrect completion state")
		}
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("Enter must exit")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("Enter did not quit")
		}
		c := validConfig(t)
		c.TXAdmin = true
		c.Adminer = true
		c.DBExposePort = true
		text := startupSummary("/servers/test", c, m.err, []string{"retained log"})
		for _, want := range []string{"fivem://connect/127.0.0.1:", "txAdmin: ", "Adminer: ", "Database host port:", "kora --server", "\x1b]8;;"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q", want)
			}
		}
		if failure != nil && (!strings.Contains(text, "retained log") || strings.Contains(text, "SERVER READY")) {
			t.Fatal("failure summary wrong")
		}
	}
}

func TestStartupStreamsDockerFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; readiness and state tests run on Windows")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'build output fixture-secret'\necho 'container failed' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "install.complete"), nil, 0600)
	os.WriteFile(filepath.Join(root, ".env"), []byte("CFX_PORT=\"30120\"\nTXADMIN_PORT=\"40120\"\nCFX_LICENSE_KEY=\"fixture-secret\"\n"), 0600)
	var lines []string
	err := startup(context.Background(), root, func(p Progress) { lines = append(lines, p.Text) })
	output := strings.Join(lines, "\n")
	if err == nil || !strings.Contains(output, "build output [redacted]") || !strings.Contains(output, "container failed") || strings.Contains(output, "fixture-secret") {
		t.Fatalf("err=%v output=%q", err, output)
	}
}
