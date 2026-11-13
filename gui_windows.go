//go:build windows && gui

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	d "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

type setupGUI struct {
	tabs                                     *walk.TabWidget
	scanned                                  bool
	window                                   *walk.MainWindow
	pages                                    [6]*walk.Composite
	heading, status                          *walk.Label
	back, next, cancel                       *walk.PushButton
	bar                                      *walk.ProgressBar
	logs, checks, review, result, serverInfo *walk.TextEdit
	edits                                    map[string]**walk.LineEdit
	choices                                  map[string]**walk.ComboBox
	tx, service, report, secrets, start      *walk.CheckBox
	existing                                 *walk.Composite
	manager                                  *walk.ScrollView
	serversBox                               *walk.ComboBox
	servers                                  []Server
	config                                   Config
	plan                                     Plan
	page                                     int
	busy, checked, complete                  bool
	folder                                   string
	cancelWork                               context.CancelFunc
	logText                                  string
}

func launchGUI(c Config) (bool, int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	g, err := newSetupGUI(c)
	if err != nil {
		walk.MsgBox(nil, "Kora could not open", err.Error(), walk.MsgBoxIconError)
		return true, 1
	}
	defer g.window.Dispose()
	g.checkComputer()
	g.window.Run()
	return true, 0
}
func guiLabel(text string) d.TextLabel {
	return d.TextLabel{Text: text, TextColor: walk.RGB(225, 230, 222), NoPrefix: true}
}
func (g *setupGUI) edit(key string) *walk.LineEdit  { return *g.edits[key] }
func (g *setupGUI) combo(key string) *walk.ComboBox { return *g.choices[key] }
func (g *setupGUI) input(key, label, value string, password bool) d.Widget {
	slot := new(*walk.LineEdit)
	g.edits[key] = slot
	return d.Composite{Layout: d.Grid{Columns: 2, MarginsZero: true}, Children: []d.Widget{
		d.Label{Text: label, TextColor: walk.RGB(225, 230, 222), MinSize: d.Size{Width: 180}, NoPrefix: true},
		d.LineEdit{AssignTo: slot, Text: value, PasswordMode: password, StretchFactor: 1, Accessibility: d.Accessibility{Name: label}},
	}}
}
func (g *setupGUI) choice(key, label string, values []string, current int) d.Widget {
	slot := new(*walk.ComboBox)
	g.choices[key] = slot
	return d.Composite{Layout: d.Grid{Columns: 2, MarginsZero: true}, Children: []d.Widget{
		d.Label{Text: label, TextColor: walk.RGB(225, 230, 222), MinSize: d.Size{Width: 180}, NoPrefix: true},
		d.ComboBox{AssignTo: slot, Model: values, CurrentIndex: current, Accessibility: d.Accessibility{Name: label}},
	}}
}
func newSetupGUI(c Config) (*setupGUI, error) {
	c.Docker = false
	c.Adminer = false
	c.DBExposePort = false
	if c.DirectoryName == "" {
		c.DirectoryName = "my-server"
	}
	g := &setupGUI{config: c, edits: map[string]**walk.LineEdit{}, choices: map[string]**walk.ComboBox{}}
	databaseMode := 0
	if c.DBSetup == "existing" {
		databaseMode = 1
	}
	sourceMode, sourcePath := 0, ""
	if c.CatalogSource != "" && c.CatalogSource != defaultCatalogURL {
		sourceMode, sourcePath = 1, c.CatalogSource
	} else if c.RecipeInput != "" && c.RecipeInput != "bundled" {
		sourceMode, sourcePath = 2, c.RecipeInput
	}
	dbType := 0
	if c.DBType == "mysql" {
		dbType = 1
	}
	page := func(i int, children ...d.Widget) d.Widget {
		return d.Composite{AssignTo: &g.pages[i], Visible: i == 0, Layout: d.VBox{}, Children: children}
	}
	tabPage := func(title string, children ...d.Widget) d.TabPage {
		return d.TabPage{Title: title, Background: d.SolidColorBrush{Color: walk.RGB(245, 245, 245)}, Layout: d.VBox{MarginsZero: true}, Children: []d.Widget{
			d.Composite{Background: d.SolidColorBrush{Color: walk.RGB(23, 23, 23)}, Layout: d.VBox{}, Children: children},
		}}
	}
	link := func(label, url string) d.Widget { return d.PushButton{Text: label, OnClicked: func() { g.open(url) }} }
	err := (d.MainWindow{
		AssignTo: &g.window, Title: "Kora Control • FiveM server management", Size: d.Size{Width: 980, Height: 820}, MinSize: d.Size{Width: 740, Height: 480},
		Background: d.SolidColorBrush{Color: walk.RGB(23, 23, 23)}, Font: d.Font{Family: "Segoe UI", PointSize: 10}, Layout: d.VBox{Margins: d.Margins{Left: 24, Top: 18, Right: 24, Bottom: 18}, Spacing: 12},
		Children: []d.Widget{
			d.Label{Text: "KORA / CONTROL", TextColor: walk.RGB(149, 255, 0), Font: d.Font{Family: "Segoe UI", PointSize: 24, Bold: true}},
			guiLabel("Your FiveM server, one clear step at a time. Native Windows. Your files stay yours."),
			d.TabWidget{AssignTo: &g.tabs, Pages: []d.TabPage{
				tabPage("Create a server",
					d.Label{AssignTo: &g.heading, Text: "1 / 5 · Check your computer", TextColor: walk.RGB(149, 255, 0), Font: d.Font{PointSize: 15, Bold: true}},
					d.ScrollView{Layout: d.VBox{}, Children: []d.Widget{
						page(0,
							guiLabel("First we check the tools FiveM needs. Nothing is installed or changed yet."),
							d.TextEdit{AssignTo: &g.checks, ReadOnly: true, VScroll: true, MinSize: d.Size{Height: 170}},
							d.Composite{Layout: d.HBox{}, Children: []d.Widget{link("Get Git for Windows", "https://git-scm.com/install/windows"), link("Get Microsoft VC++ runtime", "https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist"), d.PushButton{Text: "Check again", OnClicked: g.checkComputer}}},
							guiLabel("Creating Windows services needs Administrator permission. Restart before entering your license."),
							d.PushButton{Text: "Restart as Administrator…", OnClicked: g.elevate},
							d.PushButton{Text: "I already have a database — continue without creating services", OnClicked: func() {
								g.combo("db-setup").SetCurrentIndex(1)
								g.service.SetChecked(false)
								g.config.DBSetup = "existing"
								g.config.WindowsService = false
								g.advance()
							}},
							guiLabel("Install missing tools using these links, then reopen Kora so it sees the updated PATH.")),
						page(1,
							g.input("hostname", "Server name (players see this)", c.Hostname, false),
							g.input("output", "Parent folder", c.Output, false),
							d.PushButton{Text: "Find available ports", OnClicked: g.suggestPorts},
							d.PushButton{Text: "Browse for parent folder…", OnClicked: func() { g.browse("output", true) }},
							g.input("name", "New folder name", c.DirectoryName, false),
							guiLabel("Kora creates a NEW folder here. If the name already exists, choose another one."),
							g.input("cfx-user", "Cfx.re username", c.CFXUser, false),
							g.input("license", "Server license key", c.License, true),
							link("Create / find my server license at portal.cfx.re", "https://portal.cfx.re/"),
							guiLabel("Sign in, register a server, and paste its license above. Ctrl+V and right-click Paste work."),
							g.input("cfx-port", "FiveM port (TCP + UDP)", strconv.Itoa(c.CFXPort), false),
							d.CheckBox{Background: d.SolidColorBrush{Color: walk.RGB(245, 245, 245)}, AssignTo: &g.tx, Text: "Enable txAdmin — optional browser administration", Checked: c.TXAdmin},
							g.input("tx-port", "txAdmin port", strconv.Itoa(c.TXPort), false)),
						page(2,
							guiLabel("Recommended: let Kora create a private MariaDB service. No database expertise needed."),
							g.choice("db-setup", "Database", []string{"Create a new MariaDB service (recommended)", "Use an existing MariaDB / MySQL service"}, databaseMode),
							g.input("db-port", "Database port", strconv.Itoa(c.DBPort), false),
							guiLabel("Each new database service needs a free port. Try 3307 for a second server."),
							d.Composite{AssignTo: &g.existing, Visible: databaseMode == 1, Layout: d.VBox{}, Children: []d.Widget{
								g.choice("db-type", "Database type", []string{"MariaDB", "MySQL"}, dbType),
								g.input("db-host", "Database address", c.DBHost, false), g.input("db-admin", "Administrator username", c.DBAdmin, false), g.input("db-admin-password", "Administrator password", c.DBAdminPassword, true),
								guiLabel("Kora creates a fresh database and account. Existing credentials are never reset."),
							}},
							d.CheckBox{Background: d.SolidColorBrush{Color: walk.RGB(245, 245, 245)}, AssignTo: &g.service, Text: "Start this FiveM server automatically with Windows", Checked: c.WindowsService},
							guiLabel("Without automatic startup, use Your servers to start and stop it. Database data stays intact.")),
						page(3,
							guiLabel("You can leave everything here unchanged. Latest Kora releases and recommended FiveM artifacts are the defaults."),
							g.choice("source", "Resources", []string{"Kora recommended catalog", "Custom resources.json file or URL", "Custom txAdmin recipe file or URL"}, sourceMode),
							g.input("source-path", "Custom source (optional)", sourcePath, false),
							d.PushButton{Text: "Browse for a custom catalog or recipe…", OnClicked: func() { g.browse("source-path", false) }},
							g.choice("game-build", "Game build", []string{c.GameBuild}, 0), g.choice("artifact", "FXServer artifact", []string{c.Artifact}, 0), g.choice("kora-tag", "Kora version", []string{c.KoraTag}, 0),
							d.PushButton{Text: "Refresh available versions", OnClicked: g.refreshVersions},
							d.CheckBox{Background: d.SolidColorBrush{Color: walk.RGB(245, 245, 245)}, AssignTo: &g.report, Text: "Save report.json (no credentials)", Checked: c.Report},
							d.CheckBox{Background: d.SolidColorBrush{Color: walk.RGB(245, 245, 245)}, AssignTo: &g.secrets, Text: "Also save secrets.json (contains passwords; keep private)", Checked: c.Secrets},
							guiLabel("Recipes are validated before installation; unsupported actions stop with an explanation.")),
						page(4,
							guiLabel("Review the exact versions and destination. No server files have been installed yet."),
							d.TextEdit{AssignTo: &g.review, ReadOnly: true, VScroll: true, MinSize: d.Size{Height: 250}},
							d.CheckBox{Background: d.SolidColorBrush{Color: walk.RGB(245, 245, 245)}, AssignTo: &g.start, Text: "Start the server when installation finishes", Checked: true},
							guiLabel("Install creates files and a fresh database. Failed installs stay available for inspection.")),
						page(5,
							d.TextEdit{AssignTo: &g.result, ReadOnly: true, VScroll: true, MinSize: d.Size{Height: 230}},
							d.Composite{Layout: d.HBox{}, Children: []d.Widget{
								d.PushButton{Text: "Open server folder", OnClicked: func() {
									if g.folder != "" {
										g.open(g.folder)
									}
								}},
								d.PushButton{Text: "Open txAdmin", OnClicked: func() { g.openServerLink("txadmin") }},
								d.PushButton{Text: "Connect with FiveM", OnClicked: func() { g.openServerLink("connect") }},
							}},
							guiLabel("Ready means responding on this PC. Friends need your reachable IP and FiveM TCP + UDP ports allowed through Windows and router/provider firewalls."),
							link("FiveM connection troubleshooting", "https://docs.fivem.net/docs/server-manual/setting-up-a-server-vanilla/")),
					}},
					d.Composite{Layout: d.HBox{}, Children: []d.Widget{
						d.PushButton{AssignTo: &g.back, Text: "Back", Enabled: false, OnClicked: g.goBack}, d.HSpacer{}, d.PushButton{AssignTo: &g.next, Text: "Continue", Enabled: false, OnClicked: g.advance},
					}},
				),
				tabPage("Your servers",
					d.ScrollView{AssignTo: &g.manager, Layout: d.VBox{}, Children: []d.Widget{
						guiLabel("Manage existing native Windows servers. No delete or reset buttons."),
						d.Composite{Layout: d.HBox{}, Children: []d.Widget{d.PushButton{Text: "Find my servers", OnClicked: g.findServers}, d.PushButton{Text: "Find in another folder…", OnClicked: g.findOther}}},
						d.ComboBox{AssignTo: &g.serversBox, OnCurrentIndexChanged: g.selectServer},
						d.TextEdit{AssignTo: &g.serverInfo, ReadOnly: true, VScroll: true, MinSize: d.Size{Height: 200}},
						d.Composite{Layout: d.HBox{}, Children: []d.Widget{
							d.PushButton{Text: "Start", OnClicked: func() { g.serverAction("start") }}, d.PushButton{Text: "Stop", OnClicked: func() { g.serverAction("stop") }}, d.PushButton{Text: "Restart", OnClicked: func() { g.serverAction("restart") }}, d.PushButton{Text: "View logs", OnClicked: func() { g.serverAction("logs") }}, d.PushButton{Text: "Open folder", OnClicked: func() {
								if s, ok := g.selected(); ok {
									g.open(s.Path)
								}
							}},
						}},
						d.PushButton{Text: "Stage resource updates in a NEW folder…", OnClicked: func() { g.serverAction("update") }},
						guiLabel("Updates keep Git history and never replace live resources. Review the staged folder before adopting it."),
					}},
				),
			}},
			d.Label{AssignTo: &g.status, Text: "Welcome to Kora", TextColor: walk.RGB(149, 255, 0), NoPrefix: true},
			d.ProgressBar{AssignTo: &g.bar, MaxValue: 100},
			d.TextEdit{AssignTo: &g.logs, ReadOnly: true, VScroll: true, MinSize: d.Size{Height: 55}, MaxSize: d.Size{Height: 85}, Font: d.Font{Family: "Consolas", PointSize: 9}},
			d.Composite{Layout: d.HBox{}, Children: []d.Widget{guiLabel("Activity log • credentials are hidden"), d.HSpacer{}, d.PushButton{AssignTo: &g.cancel, Text: "Cancel current operation", Enabled: false, OnClicked: func() {
				if g.cancelWork != nil {
					g.cancelWork()
					g.status.SetText("Cancelling… please wait. Created files will be retained.")
				}
			}}, d.PushButton{Text: "Close", OnClicked: func() { g.window.Close() }}}},
		},
	}).Create()
	if err != nil {
		return nil, err
	}
	g.combo("db-setup").CurrentIndexChanged().Attach(func() { g.existing.SetVisible(g.combo("db-setup").CurrentIndex() == 1) })
	g.window.Closing().Attach(func(cancel *bool, _ walk.CloseReason) {
		if g.busy {
			*cancel = true
			g.notice("An operation is still running", "Use Cancel current operation and wait before closing. Created files and database data are retained.")
		}
	})
	g.fitToScreen()
	return g, nil
}

func (g *setupGUI) notice(title, text string) {
	walk.MsgBox(g.window, title, text, walk.MsgBoxOK|walk.MsgBoxIconInformation)
}
func (g *setupGUI) open(target string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(target)
	if err == nil {
		err = windows.ShellExecute(windows.Handle(g.window.Handle()), verb, file, nil, nil, windows.SW_SHOWNORMAL)
	}
	if err != nil {
		g.notice("Could not open automatically", "Open this yourself:\r\n"+target+"\r\n\r\n"+err.Error())
	}
}
func (g *setupGUI) elevate() {
	if nativePrivilegeCheck() == nil {
		g.notice("Already Administrator", "This window already has permission to create services.")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		g.notice("Cannot restart", err.Error())
		return
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	path, _ := windows.UTF16PtrFromString(exe)
	if err = windows.ShellExecute(windows.Handle(g.window.Handle()), verb, path, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		g.notice("Restart cancelled", "Administrator permission was not granted. You can still use an existing database without creating a FiveM service.")
		return
	}
	g.window.Close()
}
func (g *setupGUI) browse(key string, folder bool) {
	dialog := walk.FileDialog{Title: "Choose " + key, FilePath: g.edit(key).Text()}
	var ok bool
	var err error
	if folder {
		ok, err = dialog.ShowBrowseFolder(g.window)
	} else {
		dialog.Filter = "Catalog / recipe (*.json;*.yaml;*.yml)|*.json;*.yaml;*.yml|All files (*.*)|*.*"
		ok, err = dialog.ShowOpen(g.window)
	}
	if err != nil {
		g.notice("Could not choose path", err.Error())
	} else if ok {
		g.edit(key).SetText(dialog.FilePath)
	}
}
func (g *setupGUI) show(page int) {
	g.page = page
	for i, p := range g.pages {
		p.SetVisible(i == page)
	}
	titles := []string{"1 / 5 · Check your computer", "2 / 5 · Name your server", "3 / 5 · Database & startup", "4 / 5 · Optional settings", "5 / 5 · Review & install", "Installation result"}
	g.heading.SetText(titles[page])
	g.back.SetEnabled(page > 0 && page < 5 && !g.busy)
	g.next.SetEnabled(!g.busy && (page != 0 || g.checked))
	text := "Continue"
	if page == 3 {
		text = "Check settings & review"
	}
	if page == 4 {
		text = "Install server"
	}
	if page == 5 {
		text = "Set up another server"
	}
	g.next.SetText(text)
}
func (g *setupGUI) goBack() {
	if !g.busy && g.page > 0 && g.page < 5 {
		g.plan = Plan{}
		g.show(g.page - 1)
	}
}
func (g *setupGUI) readConfig() (Config, error) {
	c := g.config
	for key, dest := range map[string]*string{"hostname": &c.Hostname, "output": &c.Output, "name": &c.DirectoryName, "cfx-user": &c.CFXUser, "license": &c.License, "db-host": &c.DBHost, "db-admin": &c.DBAdmin} {
		*dest = strings.TrimSpace(g.edit(key).Text())
	}
	c.DBAdminPassword = g.edit("db-admin-password").Text()
	c.TXAdmin = g.tx.Checked()
	for key, dest := range map[string]*int{"cfx-port": &c.CFXPort, "tx-port": &c.TXPort, "db-port": &c.DBPort} {
		if key == "db-port" && g.page < 2 {
			continue
		}
		if key == "tx-port" && !c.TXAdmin {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(g.edit(key).Text()))
		if err != nil || v < 1 || v > 65535 {
			return c, fmt.Errorf("%s must be a whole number from 1 to 65535", key)
		}
		*dest = v
	}
	c.TXAdmin = g.tx.Checked()
	c.WindowsService = g.service.Checked()
	c.Report = g.report.Checked()
	c.Secrets = g.secrets.Checked()
	c.DBSetup = "new-service"
	c.DBType = "maria"
	if g.combo("db-setup").CurrentIndex() == 1 {
		c.DBSetup = "existing"
		if g.combo("db-type").CurrentIndex() == 1 {
			c.DBType = "mysql"
		}
	} else {
		c.DBHost = "127.0.0.1"
		c.DBAdmin = "root"
		c.DBAdminPassword = ""
	}
	c.CatalogSource = defaultCatalogURL
	c.RecipeInput = "bundled"
	c.Template = ""
	source := strings.TrimSpace(g.edit("source-path").Text())
	switch g.combo("source").CurrentIndex() {
	case 1:
		c.CatalogSource = source
	case 2:
		c.CatalogSource = ""
		c.RecipeInput = source
	}
	if g.page >= 3 && g.combo("source").CurrentIndex() != 0 && source == "" {
		return c, fmt.Errorf("Choose a custom source file or enter its URL, or select Kora recommended catalog")
	}
	c.GameBuild = g.combo("game-build").Text()
	c.Artifact = g.combo("artifact").Text()
	c.KoraTag = g.combo("kora-tag").Text()
	return c, nil
}
func (g *setupGUI) advance() {
	if g.busy {
		return
	}
	if g.page == 5 {
		c, err := defaults()
		if err != nil {
			g.notice("Cannot create settings", err.Error())
			return
		}
		g.config.DBName = c.DBName
		g.config.DBUser = c.DBUser
		g.config.DBPassword = c.DBPassword
		g.complete = false
		g.plan = Plan{}
		g.edit("name").SetText("my-server-" + strings.TrimPrefix(c.DBName, "kora_"))
		g.show(1)
		return
	}
	if g.page == 0 {
		if g.combo("db-setup").CurrentIndex() == 0 && nativePrivilegeCheck() != nil {
			g.notice("Administrator needed for a new database", "Click Restart as Administrator before continuing, or choose the existing database option.")
			return
		}
		if g.checked {
			g.show(1)
		}
		return
	}
	c, err := g.readConfig()
	if err == nil {
		err = guiValidate(c, g.page)
	}
	if err != nil {
		g.notice("Let's fix this first", err.Error())
		return
	}
	if g.page == 2 && (c.WindowsService || c.DBSetup == "new-service") && nativePrivilegeCheck() != nil {
		g.notice("Service creation needs Administrator permission", "Use an existing database and turn off automatic FiveM startup, or go Back to Check your computer and restart as Administrator.")
		return
	}
	g.config = c
	if g.page < 3 {
		g.show(g.page + 1)
		return
	}
	if g.page == 3 {
		g.task("Checking settings and resolving versions", func(ctx context.Context, emit func(Progress)) (func(), error) {
			checks := installationPrerequisites(ctx, c, currentHost(), runProbe)
			if !checksReady(checks) {
				return nil, fmt.Errorf("A prerequisite is missing. Go Back to Check your computer for download links")
			}
			p, err := prepare(ctx, c, emit)
			return func() {
				if err == nil {
					g.plan = p
					g.review.SetText(guiReview(p))
					g.show(4)
				}
			}, err
		})
		return
	}
	if g.page == 4 {
		p := g.plan
		start := g.start.Checked()
		g.task("Installing — keep this window open", func(ctx context.Context, emit func(Progress)) (func(), error) {
			if err := checkPorts(p.Config); err != nil {
				return nil, err
			}
			result := install(ctx, p, emit)
			err := result.Err
			ready := false
			if err == nil && start {
				emit(Progress{Text: "Starting server and waiting for a local FiveM response"})
				err = startup(ctx, result.Folder, emit)
				ready = err == nil
			}
			return func() {
				g.folder = result.Folder
				g.complete = result.Err == nil
				g.show(5)
				message := "Installed. Start it from Your servers when you are ready."
				if ready {
					message = "SERVER RESPONDING ON THIS PC"
				}
				if err != nil {
					message = "Installation / startup needs attention: " + guiRedact(err.Error(), p.Config)
				}
				message += "\r\n\r\nFolder: " + result.Folder
				if result.Err == nil {
					message += fmt.Sprintf("\r\nServer info: http://127.0.0.1:%d/info.json\r\nFiveM: fivem://connect/127.0.0.1:%d", p.Config.CFXPort, p.Config.CFXPort)
					if p.Config.TXAdmin {
						message += fmt.Sprintf("\r\ntxAdmin: http://127.0.0.1:%d", p.Config.TXPort)
					}
				} else {
					message += "\r\nFiles are retained for inspection. Correct the issue and use a NEW folder name to retry."
				}
				if result.Err == nil && err != nil {
					message += "\r\nThe installation is complete. Use Your servers to inspect logs and retry Start; do not reinstall."
				}
				g.result.SetText(message)
			}, err
		})
	}
}
func (g *setupGUI) task(title string, work func(context.Context, func(Progress)) (func(), error)) {
	if g.busy {
		return
	}
	g.busy = true
	g.status.SetText(title)
	g.bar.SetMarqueeMode(true)
	g.next.SetEnabled(false)
	g.back.SetEnabled(false)
	g.manager.SetEnabled(false)
	for _, p := range g.pages {
		p.SetEnabled(false)
	}
	g.cancel.SetEnabled(true)
	ctx, cancel := context.WithCancel(context.Background())
	g.cancelWork = cancel
	snapshot := g.config
	go func() {
		defer cancel()
		var mu sync.Mutex
		var last time.Time
		emit := func(p Progress) {
			mu.Lock()
			defer mu.Unlock()
			if time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			text := guiRedact(p.Text, snapshot)
			g.window.Synchronize(func() {
				g.logText += text + "\r\n"
				if len(g.logText) > 24000 {
					g.logText = g.logText[len(g.logText)-24000:]
				}
				g.logs.SetText(g.logText)
				if p.Total > 0 {
					g.bar.SetMarqueeMode(false)
					g.bar.SetValue(min(100, p.Done*100/p.Total))
				}
			})
		}
		finish, err := work(ctx, emit)
		g.window.Synchronize(func() {
			g.busy = false
			g.cancelWork = nil
			g.cancel.SetEnabled(false)
			g.bar.SetMarqueeMode(false)
			g.manager.SetEnabled(true)
			for _, p := range g.pages {
				p.SetEnabled(true)
			}
			if finish != nil {
				finish()
			}
			g.show(g.page)
			if err != nil {
				text := guiRedact(err.Error(), snapshot)
				g.status.SetText("Needs attention — see details")
				g.logText += "\r\n" + text
				g.logs.SetText(g.logText)
				g.notice("Let's fix this", text)
			} else {
				g.status.SetText("Done — ready for the next step")
				g.bar.SetValue(100)
			}
		})
	}()
}
func (g *setupGUI) checkComputer() {
	if g.busy {
		return
	}
	scan := !g.scanned
	g.scanned = true
	c := g.config
	g.checked = false
	g.task("Checking Windows, Git and the FiveM runtime", func(ctx context.Context, _ func(Progress)) (func(), error) {
		checks := installationPrerequisites(ctx, c, currentHost(), runProbe)
		var servers []Server
		if scan {
			servers = discoverServers(ctx, c)
		}
		return func() {
			var lines []string
			for _, check := range checks {
				mark := "OK"
				if check.Warning {
					mark = "NOTE"
				}
				if check.Blocked {
					mark = "ACTION NEEDED"
				}
				lines = append(lines, mark+" · "+check.Name+"\r\n"+check.Detail+"\r\n")
			}
			g.checks.SetText(strings.Join(lines, "\r\n"))
			g.checked = checksReady(checks)
			if scan {
				g.populateServers(servers)
				if len(g.servers) > 0 {
					g.tabs.SetCurrentIndex(1)
				}
			}
		}, nil
	})
}
func (g *setupGUI) refreshVersions() {
	c, err := g.readConfig()
	if err != nil {
		g.notice("Check optional settings", err.Error())
		return
	}
	g.task("Loading available versions", func(ctx context.Context, _ func(Progress)) (func(), error) {
		results := []versionChoicesMsg{}
		for _, key := range []string{"game-build", "artifact", "kora-tag"} {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			msg := versionChoices(key, c.CatalogSource, "windows")().(versionChoicesMsg)
			if msg.Err != nil {
				return nil, msg.Err
			}
			results = append(results, msg)
		}
		return func() {
			for _, msg := range results {
				g.combo(msg.Key).SetModel(msg.Values)
				g.combo(msg.Key).SetCurrentIndex(0)
			}
		}, nil
	})
}
func (g *setupGUI) findServers() {
	c := g.config
	g.task("Finding existing servers", func(ctx context.Context, _ func(Progress)) (func(), error) {
		servers := discoverServers(ctx, c)
		return func() {
			g.populateServers(servers)
		}, nil
	})
}
func (g *setupGUI) findOther() {
	dialog := walk.FileDialog{Title: "Choose an existing server or its parent folder"}
	if ok, err := dialog.ShowBrowseFolder(g.window); err == nil && ok {
		g.config.Output = dialog.FilePath
		g.findServers()
	}
}
func (g *setupGUI) selected() (Server, bool) {
	i := g.serversBox.CurrentIndex()
	if i < 0 || i >= len(g.servers) {
		g.notice("Choose a server", "Click Find my servers, then select a server.")
		return Server{}, false
	}
	return g.servers[i], true
}
func (g *setupGUI) selectServer() {
	i := g.serversBox.CurrentIndex()
	if i >= 0 && i < len(g.servers) {
		s := g.servers[i]
		g.serverInfo.SetText(s.Path + "\r\n" + s.Status + "\r\n" + s.DockerState)
	}
}
func (g *setupGUI) serverAction(action string) {
	s, ok := g.selected()
	if !ok {
		return
	}
	if action != "logs" && (!s.Managed || !regularFile(filepath.Join(s.Path, "install.complete"))) {
		g.notice("Incomplete installation", "This folder is available for inspection only. Correct the problem and create a new installation.")
		return
	}
	questions := map[string]string{"start": "Start this server?", "stop": "Stop this FiveM server? Database data stays intact.", "restart": "Restart this FiveM server? Connected players will disconnect.", "update": "Clone updated resources into a new staging folder? Live resources will stay unchanged."}
	if action != "logs" && walk.MsgBox(g.window, "Confirm "+action, s.Path+"\r\n\r\n"+questions[action], walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		return
	}
	g.task("Server: "+action, func(ctx context.Context, emit func(Progress)) (func(), error) {
		var err error
		message := ""
		switch action {
		case "start":
			err = startup(ctx, s.Path, emit)
		case "restart":
			err = nativeAction(ctx, s.Path, "restart")
			if err == nil {
				err = startup(ctx, s.Path, emit)
			}
		case "stop":
			err = nativeAction(ctx, s.Path, "stop")
		case "logs":
			message = nativeLogs(s.Path)
		case "update":
			var p UpdatePlan
			p, err = prepareUpdate(ctx, s, "latest", "", emit)
			if err == nil {
				r := stageUpdate(ctx, p, emit)
				err = r.Err
				message = "Staged resources: " + r.Folder + "\r\nReview before adopting; live resources have not changed."
			}
		}
		state := nativeStatus(s.Path)
		return func() { g.serverInfo.SetText(s.Path + "\r\n" + state + "\r\n\r\n" + message) }, err
	})
}
func (g *setupGUI) openServerLink(kind string) {
	if !g.complete || g.folder == "" {
		g.notice("No complete server yet", "Finish installation before using server links.")
		return
	}
	c, err := serverConfig(g.folder)
	if err != nil {
		g.notice("Cannot read server", err.Error())
		return
	}
	if kind == "txadmin" {
		if !c.TXAdmin {
			g.notice("txAdmin is off", "txAdmin was not enabled for this server.")
			return
		}
		g.open(fmt.Sprintf("http://127.0.0.1:%d", c.TXPort))
	} else {
		g.open(fmt.Sprintf("fivem://connect/127.0.0.1:%d", c.CFXPort))
	}
}

func (g *setupGUI) suggestPorts() {
	c, err := g.readConfig()
	if err != nil {
		g.notice("Check port values", err.Error())
		return
	}
	for _, item := range []struct {
		key     string
		value   *int
		enabled bool
	}{{"cfx-port", &c.CFXPort, true}, {"tx-port", &c.TXPort, c.TXAdmin}, {"db-port", &c.DBPort, c.DBSetup == "new-service"}} {
		if !item.enabled {
			continue
		}
		found := false
		for attempts := 0; attempts < 100 && *item.value <= 65535; attempts++ {
			if checkEnteredPort(c, item.key, *item.value) == nil {
				found = true
				break
			}
			*item.value++
		}
		if !found {
			g.notice("No free port found", "Enter a different starting port and try again.")
			return
		}
		g.edit(item.key).SetText(strconv.Itoa(*item.value))
	}
	g.status.SetText("Available ports selected. Kora checks them again before installation.")
}

// Keep backend helper processes out of the user's way; their output belongs in Control.
func backgroundCommand(cmd *exec.Cmd) *exec.Cmd {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	return cmd
}

func controlWorkArea(window *walk.MainWindow) (walk.Rectangle, bool) {
	info := win.MONITORINFO{CbSize: uint32(unsafe.Sizeof(win.MONITORINFO{}))}
	if !win.GetMonitorInfo(win.MonitorFromWindow(window.Handle(), win.MONITOR_DEFAULTTONEAREST), &info) {
		return walk.Rectangle{}, false
	}
	r := info.RcWork
	return walk.Rectangle{X: int(r.Left), Y: int(r.Top), Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top)}, true
}
func (g *setupGUI) fitToScreen() {
	area, ok := controlWorkArea(g.window)
	if !ok {
		return
	}
	width := min(g.window.SizePixels().Width, area.Width-16)
	height := min(g.window.SizePixels().Height, area.Height-16)
	g.window.SetMinMaxSizePixels(walk.Size{Width: min(740, width), Height: min(480, height)}, walk.Size{})
	g.window.SetBoundsPixels(walk.Rectangle{X: area.X + (area.Width-width)/2, Y: area.Y + (area.Height-height)/2, Width: width, Height: height})
}

func (g *setupGUI) populateServers(servers []Server) {
	g.servers = nil
	names := []string{}
	for _, s := range servers {
		if regularFile(filepath.Join(s.Path, "native.json")) {
			g.servers = append(g.servers, s)
			names = append(names, filepath.Base(s.Path)+" — "+s.DockerState)
		}
	}
	g.serversBox.SetModel(names)
	if len(names) > 0 {
		g.serversBox.SetCurrentIndex(0)
	} else {
		g.serverInfo.SetText("No native Windows servers found. Install one, or choose Find in another folder.")
	}
}
