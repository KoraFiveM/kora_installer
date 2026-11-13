package main

import (
	"encoding/json"
	"os"
	"testing"
)

// Optional offline recording fixture. Uses the real TUI views with demo data;
// it never probes Docker, fetches repositories, or provisions a server.
func TestRecordWalkthrough(t *testing.T) {
	output := os.Getenv("KORA_WALKTHROUGH_FRAMES")
	if output == "" {
		t.Skip("recording not requested")
	}
	c, err := defaults()
	if err != nil {
		t.Fatal(err)
	}
	c.Hostname = "Kora demo server"
	c.CFXUser = "demo-user"
	c.License = "DEMO_NOT_A_REAL_LICENSE"
	c.DBPassword = "DEMO_PASSWORD"
	c.DirectoryName = "kora-dev"
	c.Output = "~/KoraServers"
	c.DBName = "kora_demo"
	c.DBUser = "kora_demo"
	m := newModel(c, map[string]bool{})
	m.width = 100
	m.height = 38
	m.scanPending = false
	type frame struct{ Title, Content string }
	var frames []frame
	add := func(label string) {
		for i := 0; i < 4; i++ {
			m.frame++
			frames = append(frames, frame{label, m.View().Content})
		}
	}
	m.servers = []Server{{Path: "~/KoraServers/kora-dev", Status: "Complete", Managed: true, DockerState: "running(3)"}, {Path: "~/KoraServers/kora-test", Status: "Complete", Managed: true, DockerState: "exited(2)"}}
	m.stage = "servers"
	add("kora: all your servers in one dashboard")
	m.stage = "server"
	add("Start, stop, restart, logs and resource updates")
	m.stage = "server-action"
	m.serverAction = "stop"
	m.actionRoot = m.servers[0].Path
	add("Actions confirm the selected server; stopping preserves data")
	m.stage = "welcome"
	add("Create another isolated server")
	m.stage = "preflight"
	m.checks = []Check{{Name: "Host", Detail: "Linux / amd64"}, {Name: "Docker engine", Detail: "Demo: local Linux engine ready"}, {Name: "Git", Detail: "Demo: Git available"}}
	add("Readiness checks and setup help")
	for _, key := range []string{"resources", "txadmin", "db-type", "adminer", "cfx-user", "license-key", "game-build", "artifact", "kora-tag", "name"} {
		for i, f := range fields {
			if f.Key == key {
				m.step = i
				break
			}
		}
		m.loadField() // Returned async commands are intentionally not executed.
		if versionField(key) {
			m.versionLoading = false
			m.versionOptions = []string{"latest", "v0.1.1", "v0.1.0"}
			if key == "game-build" {
				m.versionOptions = []string{"latest", "3751", "3407"}
			}
			if key == "artifact" {
				m.versionOptions = []string{"recommended", "https://runtime.fivem.net/.../fx.tar.xz"}
			}
		}
		add("New server: " + fields[m.step].Title)
	}
	m.stage = "review"
	add("Review the configuration; secrets stay masked")
	m.stage = "installing"
	m.progress = Progress{Text: "DEMO: cloning pinned resource commits", Done: 2, Total: 3}
	m.history = []string{"DEMO: catalog latest selects published tag + commit", "DEMO: Git clone retains repository history", "DEMO: each server uses a new database directory"}
	add("Simulated installation progress — no services are started")
	m.stage = "start-choice"
	m.result = Result{Folder: "~/KoraServers/kora-dev"}
	add("Choose whether to start the completed server")
	m.stage = "servers"
	add("Return to kora to manage multiple servers")
	data, err := json.Marshal(frames)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, data, 0600); err != nil {
		t.Fatal(err)
	}
}
