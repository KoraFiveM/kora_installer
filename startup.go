package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type startupMsg struct{ Err error }

func startupLinks(c Config) string {
	links := []string{fmt.Sprintf("FiveM: fivem://connect/127.0.0.1:%d", c.CFXPort), fmt.Sprintf("Server info: http://127.0.0.1:%d/info.json", c.CFXPort)}
	if c.TXAdmin {
		links = append(links, fmt.Sprintf("txAdmin: http://127.0.0.1:%d", c.TXPort))
	}
	if c.Adminer {
		links = append(links, "Adminer: "+adminerURL(c))
	}
	return strings.Join(links, "\n")
}

func clickableLinks(c Config) string {
	lines := strings.Split(startupLinks(c), "\n")
	for i, line := range lines {
		label, link, _ := strings.Cut(line, ": ")
		lines[i] = label + ": \x1b]8;;" + link + "\x1b\\" + link + "\x1b]8;;\x1b\\"
	}
	return strings.Join(lines, "\n")
}

// A listening TCP port alone does not establish that FXServer is responding.
func fiveMReady(ctx context.Context, client *http.Client, address string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var info struct {
		Resources []string                   `json:"resources"`
		Vars      map[string]json.RawMessage `json:"vars"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	return err == nil && len(data) <= 1024*1024 && json.Unmarshal(data, &info) == nil && info.Resources != nil && info.Vars != nil
}

func startup(ctx context.Context, root string, emit func(Progress)) error {
	c, err := serverConfig(root)
	if err != nil {
		return err
	}
	values, err := readEnvironment(root)
	if err != nil {
		return err
	}
	clean := func(s string) string {
		s = ansi.Strip(s)
		for key, value := range values {
			if value != "" && (strings.Contains(key, "PASSWORD") || strings.Contains(key, "LICENSE_KEY")) {
				s = strings.ReplaceAll(s, value, "[redacted]")
			}
		}
		return strings.Map(func(r rune) rune {
			if r < 32 && r != '\t' {
				return -1
			}
			return r
		}, s)
	}
	emit(Progress{Done: 0, Total: 3, Text: "Building image and starting Docker services…"})
	cmd, err := dockerCommand(ctx, root, "--ansi", "never", "--progress", "plain", "up", "--build", "-d")
	if err != nil {
		return err
	}
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	finished := make(chan error, 1)
	go func() { err := cmd.Run(); writer.Close(); finished <- err }()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		emit(Progress{Done: 0, Total: 3, Text: clean(scanner.Text())})
	}
	reader.Close()
	scanErr := scanner.Err()
	if err := <-finished; err != nil {
		return fmt.Errorf("Docker startup failed: %w; inspect the logs above", err)
	}
	if scanErr != nil {
		return fmt.Errorf("reading Docker output: %w", scanErr)
	}
	emit(Progress{Done: 2, Total: 3, Text: "Services started. Waiting for FiveM /info.json; txAdmin may need initial setup."})
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	previous := ""
	for {
		probeCtx, stop := context.WithTimeout(waitCtx, 5*time.Second)
		logs, e := dockerCommand(probeCtx, root, "logs", "--no-color", "--tail", "12")
		if e == nil {
			data, logErr := logs.Output()
			if logErr == nil && string(data) != previous {
				previous = string(data)
				for _, line := range strings.Split(strings.TrimSpace(previous), "\n") {
					emit(Progress{Done: 2, Total: 3, Text: clean(line)})
				}
			}
		}
		stop()
		statusCtx, statusCancel := context.WithTimeout(waitCtx, 5*time.Second)
		status, statusErr := dockerCommand(statusCtx, root, "ps", "--all", "--format", "json")
		running := false
		if statusErr == nil {
			data, e := status.Output()
			if e == nil {
				running, statusErr = startupContainerState(data)
			} else {
				statusErr = e
			}
		}
		statusCancel()
		if statusErr != nil {
			return fmt.Errorf("checking Docker services: %w", statusErr)
		}
		if running && fiveMReady(waitCtx, client, fmt.Sprintf("http://127.0.0.1:%d/info.json", c.CFXPort)) {
			emit(Progress{Done: 3, Total: 3, Text: "FiveM is responding — ready to connect."})
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("FiveM readiness was not confirmed: %w. Services are retained. Check the logs and txAdmin, then retry from the server dashboard", waitCtx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *model) startLaunch(root string) tea.Cmd {
	m.result.Folder = root
	m.stage, m.err, m.notice, m.history, m.scroll = "starting", "", "", nil, 0
	m.progress = Progress{Total: 3, Text: "Preparing Docker startup…"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	m.cancel = cancel
	m.events = make(chan tea.Msg, 32)
	ch := m.events
	go func() {
		defer cancel()
		err := startup(ctx, root, func(p Progress) {
			select {
			case ch <- p:
			case <-ctx.Done():
			}
		})
		ch <- startupMsg{err}
	}()
	return waitEvent(ch)
}

// Compose versions return either a JSON array or one JSON object per line.
func startupContainerState(data []byte) (bool, error) {
	type service struct {
		Service, State, Health string
		ExitCode               int
	}
	var services []service
	if strings.HasPrefix(strings.TrimSpace(string(data)), "[") {
		if err := json.Unmarshal(data, &services); err != nil {
			return false, err
		}
	} else {
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		for {
			var s service
			err := decoder.Decode(&s)
			if err == io.EOF {
				break
			}
			if err != nil {
				return false, err
			}
			services = append(services, s)
		}
	}
	running := false
	for _, s := range services {
		if s.State == "exited" || s.State == "dead" || s.Health == "unhealthy" {
			return false, fmt.Errorf("%s is %s (health: %s, exit code: %d); inspect its startup logs", s.Service, s.State, s.Health, s.ExitCode)
		}
		if s.Service == "fivem" && s.State == "running" {
			running = true
		}
	}
	return running, nil
}

func startupSummary(root string, c Config, errText string, logs []string) string {
	heading := "KORA // SERVER READY"
	if errText != "" {
		heading = "KORA // STARTUP NEEDS ATTENTION"
	}
	out := "\n" + heading + "\n" + strings.Repeat("─", 48) + "\nInstallation: " + root + "\n"
	out += fmt.Sprintf("FiveM port: %d TCP + UDP\n", c.CFXPort)
	if c.DBExposePort {
		out += fmt.Sprintf("Database host port: %d\n", c.DBHostPort)
	}
	if errText != "" {
		out += "Error: " + errText + "\n\nRecent logs:\n" + strings.Join(logs, "\n") + "\n\nConfigured links (readiness not confirmed):\n"
	}
	out += clickableLinks(c) + "\n\nLinks use this host. For remote FiveM/txAdmin access, use the server IP.\nAdminer remains local to the host (or use a tunnel).\n"
	out += "Docker services and data are retained when this installer closes.\nManage this server: kora --server " + fmt.Sprintf("%q", root) + "\n"
	return out
}
