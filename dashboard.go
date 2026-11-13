package main

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func registryDirectory() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "kora", "servers")
}
func registerServer(root string) error {
	dir := registryDirectory()
	if dir == "" {
		return fmt.Errorf("user config directory unavailable")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	id, err := randomID(8)
	if err != nil {
		return err
	}
	return writeNew(dir, id+".json", jsonBytes(struct {
		Path string `json:"path"`
	}{root}), 0600)
}
func registeredServers() []string {
	dir := registryDirectory()
	if dir == "" {
		return nil
	}
	entries, _ := os.ReadDir(dir)
	var paths []string
	for i, e := range entries {
		if i >= 1000 {
			break
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || !regularFile(filepath.Join(dir, e.Name())) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || len(data) > 16384 {
			continue
		}
		var record struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(data, &record) == nil && filepath.IsAbs(record.Path) {
			paths = append(paths, record.Path)
		}
	}
	return paths
}

type dashboardTickMsg struct{}
type serverLogsMsg struct {
	Text string
	Err  error
}
type serverActionMsg struct {
	Err    error
	Action string
}

func dashboardTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return dashboardTickMsg{} })
}
func (m model) scanServers() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return serversMsg(discoverServers(ctx, m.config))
	}
}
func (m model) updateDashboard(msg tea.Msg) (model, tea.Cmd, bool) {
	switch v := msg.(type) {
	case serverLogsMsg:
		m.stage = "server-logs"
		m.notice = ""
		m.serverLogs = v.Text
		if v.Err != nil {
			m.err = "Unable to read logs; check Docker access"
		}
		return m, nil, true
	case dashboardTickMsg:
		if !m.scanPending && (m.stage == "servers" || m.stage == "server") {
			m.scanPending = true
			return m, tea.Batch(m.scanServers(), dashboardTick()), true
		}
		return m, dashboardTick(), true
	case serverActionMsg:
		m.stage = "server"
		if len(m.servers) == 0 {
			m.stage = "servers"
		}
		if v.Err != nil {
			m.err = v.Err.Error()
		} else {
			m.notice = v.Action + " completed; refreshing server status"
		}
		m.scanPending = true
		return m, m.scanServers(), true
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil, false
	}
	if (m.stage == "servers" || m.stage == "server") && key.String() == "n" {
		m.stage = "welcome"
		m.step = 0
		m.err = ""
		m.result = Result{}
		return m, m.startChecks("new"), true
	}
	if m.stage == "server" {
		if key.String() == "i" {
			return m, m.managerWork("inspect"), true
		}
		actions := map[string]string{"s": "start", "x": "stop", "b": "restart", "l": "logs"}
		if action := actions[key.String()]; action != "" {
			server := m.servers[m.serverIndex]
			if !server.Managed || !regularFile(filepath.Join(server.Path, "install.complete")) {
				m.err = "This is not a completed installer-managed server; use its original setup tooling."
				return m, nil, true
			}
			m.scroll = 0
			m.actionRoot = server.Path
			m.serverAction = action
			m.stage = "server-action"
			m.err = ""
			return m, nil, true
		}
	}
	if m.stage == "server-action" {
		if key.String() == "esc" {
			m.stage = "server"
			return m, nil, true
		}
		if key.String() == "enter" {
			root, action := m.actionRoot, m.serverAction
			if regularFile(filepath.Join(root, "native.json")) {
				if action == "logs" {
					m.stage = "server-logs"
					m.serverLogs = nativeLogs(root)
					return m, nil, true
				}
				if action == "start" {
					return m, m.startLaunch(root), true
				}
				m.stage = "server-working"
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					defer cancel()
					return serverActionMsg{Err: nativeAction(ctx, root, action), Action: action}
				}, true
			}
			if action == "logs" {
				m.stage = "server-working"
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd, err := dockerCommand(ctx, root, "logs", "--no-color", "--tail", "100")
					if err != nil {
						return serverLogsMsg{Err: err}
					}
					data, err := cmd.Output()
					return serverLogsMsg{Text: ansi.Strip(string(data)), Err: err}
				}, true
			}

			if action == "start" {
				return m, m.startLaunch(root), true
			}
			m.stage = "server-working"
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			args := []string{action}
			if action == "start" {
				args = []string{"up", "--build", "-d"}
			}
			cmd, err := dockerCommand(ctx, root, args...)
			if err != nil {
				cancel()
				m.err = err.Error()
				m.stage = "server"
				return m, nil, true
			}
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { cancel(); return serverActionMsg{err, action} }), true
		}
	}
	if m.stage == "server-logs" && key.String() == "esc" {
		m.stage = "server"
		m.scroll = 0
		return m, nil, true
	}
	if m.stage == "server-working" {
		return m, nil, true
	}
	return m, nil, false
}
