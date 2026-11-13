package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Palette from Kora's kora_theme/theme.json.
var (
	lime       = lipgloss.Color("#95FF00")
	background = lipgloss.Color("#101010")
	surface    = lipgloss.Color("#171717")
	foreground = lipgloss.Color("#F5F5F5")
	muted      = lipgloss.Color("#949494")
	border     = lipgloss.Color("#343434")
	warning    = lipgloss.Color("#F59E0B")
	accent     = lipgloss.NewStyle().Foreground(lime)
	dim        = lipgloss.NewStyle().Foreground(muted)
	title      = lipgloss.NewStyle().Foreground(foreground).Bold(true)
)

const wordmark = `██╗  ██╗ ██████╗ ██████╗  █████╗
██║ ██╔╝██╔═══██╗██╔══██╗██╔══██╗
█████╔╝ ██║   ██║██████╔╝███████║
██╔═██╗ ██║   ██║██╔══██╗██╔══██║
██║  ██╗╚██████╔╝██║  ██║██║  ██║
╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝`

type field struct {
	Key, Section, Title, Hint string
	Options                   []string
	Secret                    bool
}

var fields = []field{
	{"resources", "01 / SOURCE", "Resource catalog", "Default: docs.koraframe.work/resources.json with GitHub fallback. Paste a local file or HTTP(S)/GitHub URL.", nil, false},
	{"txadmin", "01 / HOST", "Bring txAdmin along?", "Optional web administration, monitoring and console.", []string{"no", "yes"}, false},
	{"tx-port", "01 / HOST", "txAdmin port", "Choose a free TCP port. Default: 40120.", nil, false},
	{"db-type", "02 / DATABASE", "Choose your database", "Both supported by Kora. MariaDB is the default.", []string{"maria", "mysql"}, false},
	{"db-expose-port", "02 / DATABASE", "Expose the database port on this host?", "Publish the database on all host interfaces for external tools. Default: off; protect access with your firewall.", []string{"no", "yes"}, false},
	{"db-host-port", "02 / DATABASE", "Host port for the database", "Maps this host port to container port 3306.", nil, false},
	{"adminer", "02 / DATABASE", "Add Adminer database tools?", "Optional web interface, automatically pointed at this database. Available on localhost only; uses your database credentials.", []string{"no", "yes"}, false},
	{"adminer-port", "02 / DATABASE", "Adminer web port", "Default: 8080. Open http://127.0.0.1:8080 after starting Docker. Database port publishing is not needed.", nil, false},
	{"db-auto", "02 / DATABASE", "Generate database credentials?", "Creates a unique database name, a new user and a cryptographically random password.", []string{"yes", "no"}, false},
	{"db-name", "02 / DATABASE", "New database name", "Existing databases are never reused or modified.", nil, false},
	{"db-user", "02 / DATABASE", "New database user", "Existing database accounts are never reused.", nil, false},
	{"db-password", "02 / DATABASE", "New database password", "Masked here. Stored only in private installation files.", nil, true},
	{"db-host", "02 / DATABASE", "Database host", "Native mode needs an installed MariaDB/MySQL service. Default: 127.0.0.1.", nil, false},
	{"db-port", "02 / DATABASE", "Database port", "Default: 3306. The installer will create a NEW database on this service.", nil, false},
	{"db-admin", "02 / DATABASE", "Database administrator account", "Needs CREATE DATABASE, CREATE USER, GRANT and permission to check existing accounts.", nil, false},
	{"db-admin-password", "02 / DATABASE", "Database administrator password", "Used once for provisioning. Never saved to .env, report or secrets export.", nil, true},
	{"cfx-user", "03 / CFX.RE", "Your Cfx.re username", "Press ctrl+o to open https://portal.cfx.re/ in your browser. Username is informational.", nil, false},
	{"cfx-port", "03 / CFX.RE", "FiveM port", "TCP + UDP. Default: 30120. Configure your host firewall separately.", nil, false},
	{"hostname", "03 / CFX.RE", "Give your server a name", "Autodiscovered from this machine. This is the public server display name.", nil, false},
	{"license-key", "03 / CFX.RE", "Cfx.re license key", "Create a key at https://portal.cfx.re/ · ctrl+o opens the portal.", nil, true},
	{"game-build", "04 / VERSIONS", "FiveM game build", "latest resolves from the official FiveM Legacy build list. ctrl+l lists available builds.", nil, false},
	{"artifact", "04 / VERSIONS", "FXServer artifact", "recommended uses JG Scripts. Or paste an official artifact download URL.", nil, false},
	{"kora-tag", "04 / VERSIONS", "Choose Kora resource versions", "latest = highest version-sorted tag per repo, default branch if none. Select a policy or tag.", nil, false},
	{"name", "05 / OUTPUT", "Installation directory name", "Optional name under the output directory. Leave empty for a unique kora-* folder. Existing folders are never reused.", nil, false},
	{"output", "05 / OUTPUT", "Where should your server live?", "A new kora-* folder is ALWAYS created inside this parent directory.", nil, false},
	{"report", "05 / OUTPUT", "Save report.json?", "A redacted summary with resolved resource commits and artifact URL.", []string{"no", "yes"}, false},
	{"report-path", "05 / OUTPUT", "Report location", "Relative to the new installation, e.g. reports/report.json. Never overwrites.", nil, false},
	{"secrets", "05 / OUTPUT", "Save secrets.json too?", "Optional extra copy of secrets. Private permissions; keep it private.", []string{"no", "yes"}, false},
	{"secrets-path", "05 / OUTPUT", "Secrets export location", "Relative to the new installation, e.g. private/secrets.json. Added to .gitignore.", nil, false},
	{"archive-fallback", "06 / GIT", "Git was not found", "Install Git for version tracking. Continue with the built-in HTTPS archive downloader? Archives have NO Git history.", []string{"no", "yes"}, false},
}

type preparedMsg struct {
	Plan Plan
	Err  error
}
type infoMsg string
type model struct {
	serverLogs     string
	actionRoot     string
	serverAction   string
	versionOptions []string
	versionLoading bool
	checks         []Check
	checkIndex     int
	afterChecks    string
	servers        []Server
	serverIndex    int
	inventory      []Checkout
	updatePlan     UpdatePlan
	updateResult   Result
	managerBusy    bool
	scanPending    bool
	discovered     bool
	config         Config
	seen           map[string]bool
	step           int
	stage          string
	input          textinput.Model
	choice         int
	spinner        spinner.Model
	width, height  int
	err, notice    string
	plan           Plan
	events         chan tea.Msg
	cancel         context.CancelFunc
	progress       Progress
	history        []string
	result         Result
	startChoice    int
	startupReady   bool
	scroll         int
	frame          int
	cancelled      bool
}

func newModel(c Config, seen map[string]bool) model {
	in := textinput.New()
	in.Prompt = "› "
	in.SetWidth(60)
	in.CharLimit = 4096
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accent
	m := model{config: c, seen: seen, stage: "welcome", input: in, spinner: sp, width: 90, height: 36, scanPending: true}
	if c.Server != "" {
		m.stage = "servers"
	} else if c.WriteTemplate != "" {
		m.stage = "template"
	}
	return m
}
func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, dashboardTick(), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return serversMsg(discoverServers(ctx, m.config))
	})
}
func (m model) hidden(f field) bool {
	c := m.config
	switch f.Key {
	case "resources":
		return c.CatalogSource == ""
	case "tx-port":
		return !c.TXAdmin
	case "db-host-port":
		return !c.DBExposePort
	case "adminer-port":
		return !c.Adminer
	case "db-name", "db-user", "db-password":
		return c.DBAuto
	case "db-host", "db-port", "db-admin", "db-admin-password":
		return true
	case "report-path":
		return !c.Report
	case "secrets-path":
		return !c.Secrets
	case "archive-fallback":
		if c.RecipeInput != "" {
			return true
		}
		_, e := exec.LookPath("git")
		return e == nil
	}
	return false
}
func (m model) value(k string) string {
	c := m.config
	b := func(v bool) string {
		if v {
			return "yes"
		}
		return "no"
	}
	switch k {
	case "resources":
		return c.CatalogSource
	case "txadmin":
		return b(c.TXAdmin)
	case "tx-port":
		return strconv.Itoa(c.TXPort)
	case "db-type":
		return c.DBType
	case "adminer":
		return b(c.Adminer)
	case "adminer-port":
		return strconv.Itoa(c.AdminerPort)
	case "db-expose-port":
		return b(c.DBExposePort)
	case "db-host-port":
		return strconv.Itoa(c.DBHostPort)
	case "db-auto":
		return b(c.DBAuto)
	case "db-name":
		return c.DBName
	case "db-user":
		return c.DBUser
	case "db-password":
		return c.DBPassword
	case "db-host":
		return c.DBHost
	case "db-port":
		return strconv.Itoa(c.DBPort)
	case "db-admin":
		return c.DBAdmin
	case "db-admin-password":
		return c.DBAdminPassword
	case "cfx-user":
		return c.CFXUser
	case "cfx-port":
		return strconv.Itoa(c.CFXPort)
	case "hostname":
		return c.Hostname
	case "license-key":
		return c.License
	case "game-build":
		return c.GameBuild
	case "artifact":
		return c.Artifact
	case "kora-tag":
		return c.KoraTag
	case "name":
		return c.DirectoryName
	case "output":
		return c.Output
	case "report":
		return b(c.Report)
	case "report-path":
		return c.ReportPath
	case "secrets":
		return b(c.Secrets)
	case "secrets-path":
		return c.SecretsPath
	case "archive-fallback":
		return b(c.ArchiveFallback)
	}
	return ""
}
func (m *model) set(k, v string) error {
	c := &m.config
	var number int
	switch k {
	case "tx-port", "db-port", "db-host-port", "cfx-port", "adminer-port":
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("Enter a port between 1 and 65535")
		}
		if k != "db-port" {
			if err := checkEnteredPort(m.config, k, n); err != nil {
				return err
			}
		}
		number = n
	}
	if k != "db-admin-password" && k != "name" && v == "" {
		return fmt.Errorf("This field is required")
	}
	switch k {
	case "resources":
		c.CatalogSource = v
	case "txadmin":
		c.TXAdmin = v == "yes"
	case "tx-port":
		c.TXPort = number
	case "db-type":
		c.DBType = v
	case "adminer":
		c.Adminer = v == "yes"
	case "adminer-port":
		c.AdminerPort = number
	case "db-expose-port":
		c.DBExposePort = v == "yes"
	case "db-host-port":
		c.DBHostPort = number
	case "db-auto":
		c.DBAuto = v == "yes"
	case "db-name":
		if !identifier.MatchString(v) {
			return fmt.Errorf("Use a letter followed by up to 31 letters, digits or underscores")
		}
		c.DBName = v
	case "db-user":
		if !identifier.MatchString(v) {
			return fmt.Errorf("Use a letter followed by up to 31 letters, digits or underscores")
		}
		c.DBUser = v
	case "db-password":
		c.DBPassword = v
	case "db-host":
		c.DBHost = v
	case "db-port":
		c.DBPort = number
	case "db-admin":
		c.DBAdmin = v
	case "db-admin-password":
		c.DBAdminPassword = v
	case "cfx-user":
		c.CFXUser = v
	case "cfx-port":
		c.CFXPort = number
	case "hostname":
		c.Hostname = v
	case "license-key":
		c.License = v
	case "game-build":
		c.GameBuild = v
	case "artifact":
		c.Artifact = v
	case "kora-tag":
		c.KoraTag = v
	case "name":
		c.DirectoryName = v
	case "output":
		c.Output = v
	case "report":
		c.Report = v == "yes"
	case "report-path":
		c.ReportPath = v
	case "secrets":
		c.Secrets = v == "yes"
	case "secrets-path":
		c.SecretsPath = v
	case "archive-fallback":
		if v != "yes" {
			return fmt.Errorf("Install Git, then restart the installer; or choose yes to use HTTPS archives")
		}
		c.ArchiveFallback = true
	}
	return nil
}
func (m *model) loadField() tea.Cmd {
	m.notice = ""
	m.err = ""
	m.scroll = 0
	if m.step >= len(fields) {
		m.stage = "review"
		return nil
	}
	m.stage = "form"
	f := fields[m.step]
	m.choice = 0
	for i, v := range f.Options {
		if v == m.value(f.Key) {
			m.choice = i
		}
	}
	m.input.EchoMode = textinput.EchoNormal
	if f.Secret {
		m.input.EchoMode = textinput.EchoPassword
	}
	m.input.SetValue(m.value(f.Key))
	m.input.CursorEnd()
	if versionField(f.Key) {
		m.stage = "version-select"
		m.versionOptions = nil
		m.versionLoading = true
		return versionChoices(f.Key, m.config.CatalogSource)
	}
	return m.input.Focus()
}
func (m *model) next() tea.Cmd {
	for m.step < len(fields) && (m.hidden(fields[m.step]) || m.seen[fields[m.step].Key]) {
		m.step++
	}
	return m.loadField()
}
func (m *model) back() tea.Cmd {
	if m.stage == "review" || m.stage == "ready" || m.stage == "error" {
		m.step = len(fields)
	}
	m.step--
	for m.step >= 0 && m.hidden(fields[m.step]) {
		m.step--
	}
	if m.step < 0 {
		m.step = 0
		m.stage = "welcome"
		return nil
	}
	return m.loadField()
}
func waitEvent(ch <-chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-ch } }
func (m *model) startWork(installing bool) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan tea.Msg, 32)
	ch := m.events
	c := m.config
	p := m.plan
	m.err = ""
	m.scroll = 0
	m.history = nil
	m.progress = Progress{Text: "Preparing", Total: 1}
	m.stage = "resolving"
	if installing {
		m.stage = "installing"
	}
	go func() {
		progress := func(v Progress) {
			select {
			case ch <- v:
			case <-ctx.Done():
			}
		}
		if installing {
			ch <- install(ctx, p, progress)
		} else {
			plan, e := prepare(ctx, c, progress)
			ch <- preparedMsg{plan, e}
		}
	}()
	return waitEvent(ch)
}
func browser() tea.Msg { return openURL("https://portal.cfx.re/") }
func openURL(address string) tea.Msg {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	} else {
		cmd = exec.Command("xdg-open", address)
		if host := currentHost(); host.Distro != "" || strings.Contains(strings.ToLower(host.Kernel), "microsoft") {
			if path, err := exec.LookPath("wslview"); err == nil {
				cmd = exec.Command(path, address)
			} else if path, err := exec.LookPath("explorer.exe"); err == nil {
				cmd = exec.Command(path, address)
			}
		}
	}
	if e := cmd.Start(); e != nil {
		return infoMsg("Browser unavailable. Open " + address + " manually.")
	}
	go cmd.Wait()
	return infoMsg("Opening your browser. If it does not appear: " + address)
}
func lookup(key string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		s := newSources()
		if key == "game-build" {
			v, e := s.builds(ctx)
			if e != nil {
				return infoMsg(e.Error())
			}
			return infoMsg(fmt.Sprintf("Official builds (newest first): %v", v))
		}
		tags, e := s.tags(ctx, "KoraFiveM/kora_core")
		if e != nil {
			return infoMsg(e.Error())
		}
		if len(tags) == 0 {
			return infoMsg("kora_core has no published tags; latest will use its default branch.")
		}
		if len(tags) > 15 {
			tags = tags[:15]
		}
		return infoMsg("kora_core tags: " + strings.Join(tags, ", ") + ". Exact tags must exist in every Kora repository unless overridden in the template.")
	}
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.updateDashboard(msg); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateVersions(msg); handled {
		return next, cmd
	}
	if next, cmd, handled := m.updateManager(msg); handled {
		return next, cmd
	}
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		m.input.SetWidth(inputColumns(v.Width))
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.frame++
		return m, cmd
	case infoMsg:
		m.notice = string(v)
		return m, nil
	case tea.PasteMsg:
		if m.stage == "form" && len(fields[m.step].Options) == 0 {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m, nil
	case Progress:
		m.progress = v
		m.history = append(m.history, v.Text)
		if len(m.history) > 16 {
			m.history = m.history[len(m.history)-16:]
		}
		return m, waitEvent(m.events)
	case startupMsg:
		m.stage = "startup-done"
		m.startupReady = v.Err == nil
		if v.Err != nil {
			m.err = v.Err.Error()
		}
		return m, nil
	case preparedMsg:
		if m.cancel != nil {
			m.cancel()
		}
		if v.Err != nil {
			m.err = v.Err.Error()
			m.stage = "error"
		} else {
			m.plan = v.Plan
			m.stage = "ready"
		}
		return m, nil
	case Result:
		if m.cancel != nil {
			m.cancel()
		}
		m.result = v
		m.stage = "start-choice"
		if v.Err != nil {
			m.err = v.Err.Error()
			m.stage = "done"
		}
		return m, nil
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" {
			if m.stage == "installing" || m.stage == "resolving" {
				m.cancel()
				m.notice = "Cancelling… partial files and database objects will be retained."
				return m, nil
			}
			if m.stage == "starting" {
				m.cancel()
			}
			m.cancelled = m.stage != "startup-done" && m.stage != "done" && m.stage != "start-choice"
			return m, tea.Quit
		}
		if key == "esc" && (m.stage == "form" || m.stage == "review" || m.stage == "ready" || m.stage == "error") {
			return m, m.back()
		}
		switch m.stage {
		case "startup-done":
			if key == "c" && m.startupReady {
				c, err := serverConfig(m.result.Folder)
				if err == nil {
					return m, func() tea.Msg { return openURL(fmt.Sprintf("fivem://connect/127.0.0.1:%d", c.CFXPort)) }
				}
			}
			if key == "enter" {
				return m, tea.Quit
			}
		case "starting":
			if key == "ctrl+o" {
				c, err := serverConfig(m.result.Folder)
				if err == nil && c.TXAdmin {
					return m, func() tea.Msg { return openURL(fmt.Sprintf("http://127.0.0.1:%d", c.TXPort)) }
				}
			}
		case "welcome":
			if key == "enter" {
				return m, m.startChecks("new")
			}
		case "form":
			f := fields[m.step]
			if key == "ctrl+o" && (f.Key == "cfx-user" || f.Key == "license-key") {
				return m, browser
			}
			if key == "ctrl+l" && (f.Key == "game-build" || f.Key == "kora-tag") {
				m.notice = "Looking up upstream versions…"
				return m, lookup(f.Key)
			}
			if len(f.Options) > 0 {
				switch key {
				case "left", "up", "h":
					m.choice = (m.choice + len(f.Options) - 1) % len(f.Options)
				case "right", "down", "l", "tab":
					m.choice = (m.choice + 1) % len(f.Options)
				}
			}
			if key == "enter" {
				value := m.input.Value()
				if len(f.Options) > 0 {
					value = f.Options[m.choice]
				}
				if e := m.set(f.Key, value); e != nil {
					m.err = e.Error()
					return m, nil
				}
				m.step++
				return m, m.next()
			}
			if len(f.Options) == 0 {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		case "review":
			if key == "enter" {
				if e := m.config.Validate(); e != nil {
					m.err = e.Error()
					return m, nil
				}
				if e := checkPorts(m.config); e != nil {
					m.err = e.Error()
					return m, nil
				}
				return m, m.startWork(false)
			}
			if key == "e" {
				m.seen = map[string]bool{}
				m.step = 0
				return m, m.next()
			}
		case "ready":
			if key == "enter" {
				return m, m.startWork(true)
			}
			if key == "e" {
				m.stage = "review"
				m.scroll = 0
			}
		case "error":
			if key == "enter" {
				return m, m.startWork(false)
			}
			if key == "e" {
				m.stage = "review"
			}
		case "done":
			if key == "enter" || key == "q" {
				return m, tea.Quit
			}
		case "start-choice":
			if key == "left" || key == "right" || key == "tab" {
				m.startChoice = 1 - m.startChoice
			}
			if key == "enter" {
				if m.startChoice == 1 {
					return m, m.startChecks("launch")
				}
				return m, tea.Quit
			}
		}
		if key == "pgdown" || key == "down" {
			m.scroll++
		}
		if key == "pgup" || key == "up" {
			m.scroll = max(0, m.scroll-1)
		}
	}
	// Clipboard replies and cursor blink messages are asynchronous component messages.
	if m.stage == "form" && len(fields[m.step].Options) == 0 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}
func (m model) summary(c Config) string {
	mode := "Native · " + runtime.GOOS
	if c.Docker {
		mode = "Docker · Linux containers"
	}
	tx := "Disabled"
	if c.TXAdmin {
		tx = fmt.Sprintf("Enabled · port %d", c.TXPort)
	}
	rows := [][2]string{{"HOST", c.Hostname}, {"RUNTIME", mode}, {"TXADMIN", tx}, {"FIVEM", fmt.Sprintf("TCP + UDP / %d", c.CFXPort)}, {"DATABASE", c.DBType + " / " + c.DBName}, {"ADMINER", yesPath(c.Adminer, fmt.Sprintf("http://127.0.0.1:%d", c.AdminerPort))}, {"DB HOST PORT", yesPath(c.DBExposePort, strconv.Itoa(c.DBHostPort))}, {"DB USER", c.DBUser + " · new account"}, {"CFX ACCOUNT", c.CFXUser}, {"SECRETS", "License + passwords hidden"}, {"GAME BUILD", c.GameBuild}, {"KORA VERSION", c.KoraTag}, {"DESTINATION", c.Output + " / " + installationName(c)}, {"REPORT", yesPath(c.Report, c.ReportPath)}, {"SECRETS EXPORT", yesPath(c.Secrets, c.SecretsPath)}}
	if c.CatalogSource != "" {
		rows = append(rows, [2]string{"CATALOG", c.CatalogSource})
	}
	if c.RecipeInput != "" {
		rows = append(rows, [2]string{"TXADMIN RECIPE", c.RecipeInput})
	}
	if c.Template != "" {
		rows = append(rows, [2]string{"TEMPLATE", c.Template})
	}
	width := max(20, min(66, m.width-24))
	lines := []string{accent.Render("CONFIGURATION                  VALUE"), dim.Render(strings.Repeat("─", width))}
	for _, r := range rows {
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, dim.Width(17).Render(r[0]), lipgloss.NewStyle().Width(max(12, width-17)).Foreground(foreground).Render(r[1])))
	}
	return strings.Join(lines, "\n")
}
func yesPath(on bool, p string) string {
	if on {
		return p
	}
	return "Not requested"
}
func (m model) View() tea.View {
	width := panelColumns(m.width)
	m.input.SetWidth(inputColumns(m.width))
	m.input, _ = m.input.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	var content string
	footer := "enter continue   esc back   ctrl+c exit"
	badge := lipgloss.NewStyle().Background(lime).Foreground(background).Bold(true).Padding(0, 1).Render(" KORA FRAMEWORK ")
	header := badge
	if width > 45 {
		header += dim.Render("  /  SERVER CONTROL")
	}
	if width > 45 {
		glyphs := []string{"0101  1010  0011", "1010  0110  1100", "0011  1101  0101", "1100  0011  1010"}
		header += "  " + accent.Render(glyphs[m.frame%len(glyphs)])
	}
	switch m.stage {
	case "version-select":
		content = m.versionView()
		footer = "↑/↓ select   enter confirm   r refresh   c custom   esc back"
	case "welcome":
		content = accent.Render(wordmark) + "\n\n" + title.Render("INITIALIZE // KORA SERVER") + "\n" +
			accent.Render("01") + "  Host and database\n" + accent.Render("02") + "  Cfx.re and versions\n" +
			accent.Render("03") + "  Review exact versions\n" + accent.Render("04") + "  Download and clone\n" +
			accent.Render("05") + "  Launch the generated server" + "\n\n" + dim.Render(fmt.Sprintf("%d existing servers found · s to inspect", len(m.servers)))
		footer = "enter new server   s existing servers   t base template"
	case "form":
		f := fields[m.step]
		fill := int(float64(m.step) / float64(len(fields)) * float64(width-6))
		content = dim.Render(f.Section) + "\n" + accent.Render(strings.Repeat("━", fill)) + dim.Render(strings.Repeat("─", max(0, width-6-fill))) + "\n\n" + title.Render(f.Title) + "\n\n" + lipgloss.NewStyle().Width(width-6).Foreground(muted).Render(f.Hint) + "\n\n"
		if len(f.Options) > 0 {
			cards := []string{}
			for i, v := range f.Options {
				style := lipgloss.NewStyle().Padding(1, 3).Foreground(muted).Background(surface)
				prefix := "○ "
				if i == m.choice {
					style = style.Foreground(background).Background(lime).Bold(true)
					prefix = "● "
				}
				cards = append(cards, style.MarginRight(2).Render(prefix+v))
			}
			content += joinCards(cards, width-6)
			footer = "←/→ choose   enter confirm   esc back   ctrl+c exit"
		} else {
			content += lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lime).Padding(1, 2).Width(width - 6).Render(m.input.View())
		}
		content += "\n\n" + dim.Render(fmt.Sprintf("STEP %02d / %02d", m.step+1, len(fields)))
	case "review":
		content = title.Render("CHECK CONFIG // NOTHING INSTALLED YET") + "\n\n" + m.summary(m.config) + "\n\n" + dim.Render("Next: resolve exact versions. No installation changes yet.")
		footer = "enter resolve versions   e edit all   ↑/↓ scroll   esc back"
	case "ready":
		content = title.Render("LOCKED VERSIONS // READY TO INSTALL") + "\n" + accent.Render("LIVE LOCK  GAME BUILD "+m.plan.Config.GameBuild+"  /  ARTIFACT "+m.plan.ArtifactURL) + "\n\n" + m.summary(m.plan.Config) + "\n\n" + accent.Render(fmt.Sprintf("%d repositories pinned to exact commits", len(m.plan.Resources))) + "\n" + dim.Render("Artifact: "+m.plan.ArtifactURL) + "\n\n"
		if m.plan.TXRecipe != nil {
			content += dim.Render("Recipe mode: Git clones; config adapted to private credentials.\nCreate/select-only SQL maps to a fresh Docker database.\nNo owner principals are added automatically. Source repos retained.") + "\n\n"
		}
		if !m.plan.Git {
			content += lipgloss.NewStyle().Foreground(warning).Render("ARCHIVE MODE · Git tracking will not be available.") + "\n"
		}
		content += dim.Render("Enter downloads artifacts and clones resources. Docker creates\nthe database when you choose to start the server after installation.\nThe selected host ports must be free before startup.") + "\n\n"
		for _, r := range m.plan.Resources {
			content += dim.Render(r.Path) + "  " + accent.Render(r.Ref) + dim.Render("  "+r.Commit[:12]) + "\n"
			if r.SelectionNote != "" {
				content += dim.Render("  "+r.SelectionNote) + "\n"
			}
		}
		footer = "enter install   e review   ↑/↓ scroll   ctrl+c exit"
	case "starting", "startup-done":
		heading := "STARTUP // " + m.spinner.View()
		if m.stage == "startup-done" {
			heading = "STARTUP NEEDS ATTENTION"
			if m.startupReady {
				heading = "READY TO CONNECT"
			}
		}
		n := (width - 6) * m.progress.Done / 3
		content = title.Render(heading) + "\n" + accent.Render(strings.Repeat("━", n)) + dim.Render(strings.Repeat("─", max(0, width-6-n))) + "\n" + dim.Render("Build + start → FiveM response (3 milestones)") + "\n\n"
		if c, err := serverConfig(m.result.Folder); err == nil {
			content += clickableLinks(c) + "\n" + dim.Render("Links use this host. From another PC, replace 127.0.0.1 with the server IP.") + "\n\n"
		}
		count := max(2, min(8, m.height-23))
		for _, line := range m.history[max(0, len(m.history)-count):] {
			content += dim.Render("> "+line) + "\n"
		}
		footer = "ctrl+o open txAdmin   ctrl+c leave (services stay running)"
		if m.stage == "startup-done" {
			footer = "enter close installer   c connect in FiveM"
		}
	case "resolving", "installing":
		heading := "Finding the right pieces."
		if m.stage == "installing" {
			heading = "Building your new world."
		}
		n := 0
		if m.progress.Total > 0 {
			n = (width - 8) * m.progress.Done / m.progress.Total
		}
		content = accent.Render("010110  ") + m.spinner.View() + "  " + title.Render(heading) + "\n\n" + accent.Render(strings.Repeat("━", n)) + dim.Render(strings.Repeat("─", max(0, width-8-n))) + "\n\n" + title.Render(m.progress.Text) + "\n\n"
		for _, line := range m.history {
			content += dim.Render("  > "+line) + "\n"
		}
		footer = "ctrl+c cancel safely · partial work is preserved"
	case "error":
		content = title.Render("Let's fix that before continuing.") + "\n\n" + dim.Render("No installation has been started. Check settings, connectivity\nand access to the Kora GitHub repositories.")
		footer = "enter retry   e review settings   esc back   ctrl+c exit"
	case "done":
		if m.result.Err != nil {
			content = lipgloss.NewStyle().Foreground(warning).Render("INSTALL FAILED // "+m.err) + "\n\n" + dim.Render("This directory is incomplete and cannot be started.\nA new attempt creates another directory and retains this one for inspection.")
		} else {
			content = accent.Render("✓  YOUR WORLD IS READY") + "\n\n" + title.Render("Kora is installed.") + "\n\n" + dim.Render("To start, open the installation directory and run:\n\n  kora_installer --server <installation directory>\n\nReview each resource's build/dependency instructions first.")
		}
		content += "\n\n" + title.Render("INSTALLATION") + "\n" + dim.Render(m.result.Folder)
		footer = "enter finish   ↑/↓ scroll"
	case "start-choice":
		content = accent.Render("INSTALL COMPLETE // "+m.result.Folder) + "\n\n" +
			title.Render("Start the server now?") + "\n\n" +
			dim.Render("Docker will build the image and show live FXServer logs.\nExisting data in this installation is retained. Selected ports must be free.") + "\n\n"
		if m.config.Adminer {
			content += dim.Width(width-6).Render("Adminer after startup: "+adminerURL(m.config)+"\nPassword: use DB_PASSWORD from this installation’s .env.") + "\n\n"
		}
		choices := []string{"○ No, finish", "○ Yes, start Docker"}
		choices[m.startChoice] = strings.Replace(choices[m.startChoice], "○", "●", 1)
		cards := []string{}
		for i, choice := range choices {
			style := lipgloss.NewStyle().Padding(1, 2).Foreground(muted).Background(surface)
			if i == m.startChoice {
				style = style.Foreground(background).Background(lime).Bold(true)
			}
			cards = append(cards, style.MarginRight(2).Render(choice))
		}
		content += joinCards(cards, width-6)
		footer = "←/→ choose   enter confirm"
	}
	if managed, help, ok := m.managerView(width); ok {
		content, footer = managed, help
	}
	if m.notice != "" {
		content += "\n\n" + lipgloss.NewStyle().Foreground(lime).Width(width-6).Render(m.notice)
	}
	if m.err != "" && m.stage != "done" {
		content += "\n\n" + lipgloss.NewStyle().Foreground(warning).Width(width-6).Render(m.err)
	}
	// Clip long review lists with a keyboard-controlled viewport. Narrow terminals
	// keep the active input visible without relying on cursor-position queries.
	content = lipgloss.NewStyle().MaxWidth(max(10, width-6)).Render(content)
	lines := strings.Split(content, "\n")
	available := max(6, m.height-9)
	start := min(m.scroll, max(0, len(lines)-available))
	if (m.stage == "form" || m.stage == "installing" || m.stage == "resolving") && len(lines) > available {
		start = max(0, len(lines)-available)
	}
	end := min(len(lines), start+available)
	content = strings.Join(lines[start:end], "\n")
	panel := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Background(surface).Foreground(foreground).Padding(1, 2).Width(width).Render(content)
	view := lipgloss.NewStyle().Padding(1, 2).Render(header + "\n\n" + panel + "\n" + lipgloss.NewStyle().Foreground(muted).Width(width).Render(footer))
	v := tea.NewView(view)
	v.AltScreen = true
	v.BackgroundColor = background
	v.ForegroundColor = foreground
	return v
}

func joinCards(cards []string, width int) string {
	horizontal := lipgloss.JoinHorizontal(lipgloss.Top, cards...)
	if lipgloss.Width(horizontal) <= width {
		return horizontal
	}
	return lipgloss.JoinVertical(lipgloss.Left, cards...)
}

func installationName(c Config) string {
	if c.DirectoryName != "" {
		return c.DirectoryName
	}
	return "kora-*"
}

// Lip Gloss v2 Width includes padding and borders. Keep every nested frame
// within the parent content area; reserve the textinput prompt and cursor too.
func panelColumns(terminal int) int { return max(20, min(78, terminal-12)) }
func inputColumns(terminal int) int { return max(1, panelColumns(terminal)-15) }
