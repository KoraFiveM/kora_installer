package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type serversMsg []Server
type checksMsg []Check
type launchCheckedMsg struct{ Err error }
type managerMsg struct {
	Kind      string
	Inventory []Checkout
	Plan      UpdatePlan
	Result    Result
	Err       error
}

func (m *model) startChecks(after string) tea.Cmd {
	m.stage, m.afterChecks, m.err, m.notice, m.scroll = "checking", after, "", "", 0
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	m.cancel = cancel
	c := m.config
	if after == "launch" {
		if saved, err := serverConfig(m.result.Folder); err == nil {
			c = saved
		}
	}
	return func() tea.Msg {
		defer cancel()
		return checksMsg(installationPrerequisites(ctx, c, currentHost(), runProbe))
	}
}

func (m *model) managerWork(kind string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.managerBusy = cancel, true
	m.stage, m.err, m.notice, m.scroll = "resolving", "", "", 0
	m.history = nil
	m.events = make(chan tea.Msg, 32)
	ch, server, plan, tag := m.events, m.servers[m.serverIndex], m.updatePlan, m.config.KoraTag
	catalogSource := ""
	if m.seen["resources"] {
		catalogSource = m.config.CatalogSource
	}
	go func() {
		defer cancel()
		progress := func(v Progress) {
			select {
			case ch <- v:
			case <-ctx.Done():
			}
		}
		msg := managerMsg{Kind: kind}
		switch kind {
		case "inspect":
			msg.Inventory, msg.Err = inspectServer(ctx, server, progress)
		case "resolve":
			msg.Plan, msg.Err = prepareUpdate(ctx, server, tag, catalogSource, progress)
		case "stage":
			msg.Result = stageUpdate(ctx, plan, progress)
			msg.Err = msg.Result.Err
		}
		ch <- msg
	}()
	return waitEvent(ch)
}

func (m model) updateManager(msg tea.Msg) (model, tea.Cmd, bool) {
	switch v := msg.(type) {
	case serversMsg:
		selected := ""
		if m.serverIndex < len(m.servers) {
			selected = m.servers[m.serverIndex].Path
		}
		m.scanPending = false
		m.servers = []Server(v)
		for i, server := range m.servers {
			if server.Path == selected {
				m.serverIndex = i
			}
		}
		if len(m.servers) == 0 && m.stage == "server" {
			m.stage = "servers"
		}
		if !m.discovered {
			m.discovered = true
			if m.stage == "welcome" && len(m.servers) > 0 && len(m.seen) == 0 {
				m.stage = "servers"
			}
			if m.config.Server != "" && m.stage == "servers" {
				m.stage = "servers"
				wanted, _ := filepath.Abs(m.config.Server)
				wanted, _ = filepath.EvalSymlinks(wanted)
				matched := false
				for i, server := range m.servers {
					if server.Path == wanted {
						m.serverIndex = i
						matched = true
					}
				}
				if !matched {
					m.err = "That path is not a recognized Kora server. Choose a directory containing compose.yaml and the installer files or resources.manifest."
				}

			}
		}
		m.serverIndex = min(m.serverIndex, max(0, len(m.servers)-1))
		return m, nil, true
	case checksMsg:
		m.checks, m.stage, m.checkIndex = []Check(v), "preflight", 0
		if m.config.RecipeInput != "" {
			for i := range m.checks {
				if m.checks[i].Name == "Git" && m.checks[i].Warning {
					m.checks[i].Blocked = true
					m.checks[i].Detail = "Git is required for txAdmin recipes. Install Git, reopen the terminal, and retry. Archive fallback is unavailable."
				}
			}
		}
		for i, c := range m.checks {
			if c.Blocked {
				m.checkIndex = i
				break
			}
		}
		m.scroll = 0
		return m, nil, true
	case launchCheckedMsg:
		m.stage = "start-choice"
		if v.Err != nil {
			m.err = v.Err.Error()
			return m, nil, true
		}
		return m, m.startLaunch(m.result.Folder), true
	case managerMsg:
		m.managerBusy, m.stage, m.scroll = false, "server", 0
		if v.Err != nil {
			m.err = v.Err.Error()
			if v.Kind == "stage" {
				m.stage = "update-done"
				m.updateResult = v.Result
			}
			return m, nil, true
		}
		switch v.Kind {
		case "inspect":
			m.inventory = v.Inventory
		case "resolve":
			m.updatePlan, m.stage = v.Plan, "update-review"
		case "stage":
			m.updateResult, m.stage = v.Result, "update-done"
		}
		return m, nil, true
	}
	v, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil, false
	}
	key := v.String()
	if m.stage == "welcome" {
		if key == "s" {
			m.stage, m.scroll = "servers", 0
			return m, nil, true
		}
		if key == "t" {
			m.stage, m.scroll = "template", 0
			return m, nil, true
		}
	}
	if m.stage == "checking" || m.stage == "launch-check" {
		if key == "ctrl+c" {
			m.cancel()
			m.cancelled = true
			return m, tea.Quit, true
		}
		return m, nil, true
	}
	if m.stage == "preflight" {
		switch key {
		case "r":
			return m, m.startChecks(m.afterChecks), true
		case "up":
			m.checkIndex = max(0, m.checkIndex-1)
			m.scroll = 0
		case "down", "tab":
			m.checkIndex = min(len(m.checks)-1, m.checkIndex+1)
			m.scroll = 0
		case "o", "ctrl+o":
			if link := m.checks[m.checkIndex].Help; link != "" {
				return m, func() tea.Msg { return openURL(link) }, true
			}
		case "enter":
			if !checksReady(m.checks) {
				m.notice = "Fix blocked checks, then press r to retry here."
				return m, nil, true
			}
			if m.afterChecks == "new" {
				return m, m.next(), true
			}
			m.stage = "launch-check"
			root := m.result.Folder
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			m.cancel = cancel
			return m, func() tea.Msg {
				defer cancel()
				c, err := serverConfig(root)
				if err == nil {
					err = checkPorts(c)
				}
				if err == nil && c.Docker {
					cmd, e := dockerCommand(ctx, root, "config", "--quiet")
					err = e
					if err == nil {
						if e := cmd.Run(); e != nil {
							err = fmt.Errorf("Compose configuration failed validation; inspect the installation's compose.yaml and .env")
						}
					}
				}
				return launchCheckedMsg{err}
			}, true
		case "esc":
			m.stage, m.scroll = "welcome", 0
			if m.afterChecks == "launch" {
				m.stage = "start-choice"
			}
		default:
			return m, nil, false
		}
		return m, nil, true
	}
	switch m.stage {
	case "servers":
		if m.scanPending && (key == "enter" || key == "r") {
			return m, nil, true
		}
		switch key {
		case "up":
			m.serverIndex = max(0, m.serverIndex-1)
		case "down", "tab":
			m.serverIndex = min(max(0, len(m.servers)-1), m.serverIndex+1)
		case "r":
			m.scanPending = true
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				return serversMsg(discoverServers(ctx, m.config))
			}, true
		case "enter":
			if len(m.servers) > 0 {
				m.inventory = nil
				m.stage = "server"
				m.scroll = 0
				m.err = ""
				return m, nil, true
			}
		case "esc":
			m.stage = "welcome"
		default:
			return m, nil, false
		}
		m.scroll = m.serverIndex * 3
		return m, nil, true
	case "server":
		switch key {
		case "u":
			return m, m.managerWork("resolve"), true
		case "r":
			return m, m.managerWork("inspect"), true
		case "s":
			server := m.servers[m.serverIndex]
			if !server.Managed || !regularFile(filepath.Join(server.Path, "install.complete")) {
				m.err = "Only completed installer servers can launch here. Original setups use their up.sh; incomplete installs need a fresh installation."
				return m, nil, true
			}
			c, err := serverConfig(server.Path)
			if err != nil {
				m.err = err.Error()
				return m, nil, true
			}
			m.config.Adminer, m.config.AdminerPort, m.config.DBName, m.config.DBUser = c.Adminer, c.AdminerPort, c.DBName, c.DBUser
			m.result = Result{Folder: server.Path}
			m.startChoice = 0
			m.stage = "start-choice"
			m.err = ""
		case "t":
			m.stage = "template"
		case "esc":
			m.stage = "servers"
		default:
			return m, nil, false
		}
		m.scroll = 0
		return m, nil, true
	case "update-review":
		if key == "enter" {
			return m, m.managerWork("stage"), true
		}
		if key == "esc" {
			m.stage, m.scroll = "server", 0
			return m, nil, true
		}
	case "update-done":
		if key == "enter" || key == "esc" {
			m.stage, m.err, m.scroll = "server", "", 0
			return m, nil, true
		}
	case "template":
		if key == "enter" {
			path := m.config.WriteTemplate
			if path == "" {
				path = "kora-template.json"
			}
			if err := exportTemplate(path, nil, nil); err != nil {
				m.err = err.Error()
			} else {
				m.notice = "Created " + path + ". Customize resources/ref/ensure, then use --template=<path>."
				m.err = ""
			}
			return m, nil, true
		}
		if key == "esc" {
			m.stage, m.err, m.scroll = "welcome", "", 0
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m model) managerView(width int) (string, string, bool) {
	wrap := func(s string) string { return lipgloss.NewStyle().Width(max(10, width-8)).Render(s) }
	switch m.stage {
	case "server-logs":
		return title.Render("SERVER LOGS") + "\n\n" + wrap(m.serverLogs), "↑/↓ scroll   esc back", true
	case "server-action":
		return title.Render(strings.ToUpper(m.serverAction)+" SERVER?") + "\n\n" + wrap(m.actionRoot) + "\n\n" + wrap("This affects only the selected installation. Stop preserves its containers, resources and database. Logs show the last 100 lines."), "enter confirm   esc cancel", true
	case "server-working":
		return m.spinner.View() + " Applying server action…", "Please wait; Commands have a timeout", true
	case "checking", "launch-check":
		return m.spinner.View() + "  Verifying host prerequisites…\n\n" + dim.Render("Read-only checks; each command has a timeout."), "ctrl+c cancel", true
	case "preflight":
		out := title.Render("SYSTEM CHECK") + "\n\n"
		for i, c := range m.checks {
			state := "OK"
			if c.Warning {
				state = "NOTE"
			}
			if c.Blocked {
				state = "FIX"
			}
			prefix := "  "
			if i == m.checkIndex {
				prefix = "› "
			}
			out += accent.Render(prefix+state+"  "+c.Name) + "\n"
		}
		if len(m.checks) > 0 {
			selected := m.checks[m.checkIndex]
			out += "\n" + wrap(selected.Detail)
			if selected.Help != "" {
				out += "\n\n" + dim.Render(wrap(selected.Help))
			}
		}
		return out, "↑/↓ select   o open help   r retry   enter continue   esc back", true
	case "servers":
		out := title.Render("KORA // YOUR SERVERS") + "\n\n"
		if m.scanPending {
			out += m.spinner.View() + " Scanning known server locations…\n\n"
		}
		if len(m.servers) == 0 && !m.scanPending {
			out += wrap("No servers found. Scans the output directory, nearby checkout directories, and Docker Compose projects. Use --server=<path> for another location.")
		}
		for i, s := range m.servers {
			prefix := "  "
			if i == m.serverIndex {
				prefix = "› "
			}
			out += accent.Render(prefix+filepath.Base(s.Path)+" · "+s.DockerState+" · "+s.Status) + "\n" + wrap(s.Path) + "\n\n"
		}
		return out, "↑/↓ select   enter manage   n new server   r refresh   esc home", true
	case "server":
		s := m.servers[m.serverIndex]
		out := title.Render(filepath.Base(s.Path)+" // "+s.DockerState+" · "+s.Status) + "\n" + wrap(s.Path) + "\n\n" + dim.Render(wrap("Update target: "+m.config.KoraTag+" (set --kora-tag to vary). Updates create a separate resource snapshot for review. Local files and databases stay untouched.")) + "\n\n"
		for _, r := range m.inventory {
			sha := r.Current
			if len(sha) > 12 {
				sha = sha[:12]
			}
			out += accent.Render(r.Path) + "  " + sha + "\n" + wrap(r.State) + "\n"
		}
		return out, "s start   x stop   b restart   l logs   i resources   u update   n new   esc back", true
	case "update-review":
		out := title.Render("REVIEW RESOURCE UPDATE // SEPARATE SNAPSHOT") + "\n\n" + wrap("Live mounts will not switch automatically. Ignored configuration, dependencies and build outputs are not copied. Review these and resource migration requirements before adoption.") + "\n\n"
		for _, r := range m.updatePlan.Resources {
			out += accent.Render(r.Path) + "\n" + wrap(shortCommit(r.Current)+" → "+shortCommit(r.Commit)+" / "+r.Ref) + "\n"
			if r.SelectionNote != "" {
				out += wrap(r.SelectionNote) + "\n"
			}
		}
		return out, "enter stage new resource tree   ↑/↓ scroll   esc cancel", true
	case "update-done":
		state := "SNAPSHOT READY // NOT ACTIVATED"
		if m.updateResult.Err != nil {
			state = "UPDATE STOPPED // PARTIAL SNAPSHOT RETAINED"
		}
		return title.Render(state) + "\n\n" + wrap(m.updateResult.Folder) + "\n\n" + wrap("The active server and database are unchanged. Successful snapshots include REVIEW.txt with adoption and rollback guidance. Keep the old resource tree."), "enter return to server", true
	case "template":
		path := m.config.WriteTemplate
		if path == "" {
			path = "kora-template.json"
		}
		return title.Render("BASE TEMPLATE // MAKE IT YOURS") + "\n\n" + wrap("Export the bundled Kora resource list to a new JSON file. Change refs, add custom repositories and ensure entries. Apply it only with --template=<path> on a new install. This never overwrites an existing file.") + "\n\n" + accent.Render(wrap(path)) + "\n\n" + dim.Render(wrap("Set --write-template=<new-path> to choose another destination. Completed installs also include a base-template.json with their selected versions.")), "enter create template   esc home", true
	}
	return "", "", false
}

func shortCommit(s string) string { return strings.TrimSpace(s[:min(len(s), 12)]) }
