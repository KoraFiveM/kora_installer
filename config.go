package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	WindowsService  bool
	DBSetup         string
	MariaDBVersion  string
	CatalogSource   string
	DirectoryName   string
	Yes             bool
	Start           bool
	RecipeInput     string
	Server          string `json:"-"`
	WriteTemplate   string `json:"-"`
	Docker          bool
	TXAdmin         bool
	TXPort          int
	Adminer         bool
	AdminerPort     int
	DBType          string
	DBAuto          bool
	DBExposePort    bool
	DBHostPort      int
	DBName          string
	DBUser          string
	DBPassword      string `json:"-"`
	DBHost          string
	DBPort          int
	DBAdmin         string `json:"-"`
	DBAdminPassword string `json:"-"`
	CFXUser         string
	CFXPort         int
	Hostname        string
	License         string `json:"-"`
	GameBuild       string
	Artifact        string
	KoraTag         string
	Output          string
	Report          bool
	ReportPath      string
	Secrets         bool
	SecretsPath     string
	ArchiveFallback bool
	Template        string
}

type Resource struct {
	SelectionNote string `json:"selection_note,omitempty"`
	Path          string `json:"path"`
	Repository    string `json:"repository"`
	Ref           string `json:"ref,omitempty"`
	Commit        string `json:"commit,omitempty"`
}
type Recipe struct {
	Replace   bool       `json:"replace,omitempty"`
	Version   int        `json:"version"`
	Resources []Resource `json:"resources"`
	Ensure    []string   `json:"ensure"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,31}$`)
var resourceName = regexp.MustCompile(`^[a-zA-Z0-9_\[\].-]+$`)

func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func defaults() (Config, error) {
	id, err := randomID(5)
	if err != nil {
		return Config{}, err
	}
	pass, err := randomID(24)
	if err != nil {
		return Config{}, err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "Kora Server"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	return Config{CatalogSource: defaultCatalogURL, RecipeInput: "bundled", Docker: runtime.GOOS != "windows", DBSetup: nativeDBDefault(), MariaDBVersion: "11.4.13", TXPort: 40120, AdminerPort: 8080, DBType: "maria", DBAuto: true, DBHostPort: 3306, DBName: "kora_" + id, DBUser: "kora_" + id, DBPassword: pass, DBHost: "127.0.0.1", DBPort: 3306, DBAdmin: "root", CFXPort: 30120, Hostname: host, GameBuild: "latest", Artifact: "recommended", KoraTag: "latest", Output: filepath.Join(home, "KoraServers"), ReportPath: "report.json", SecretsPath: "secrets.json"}, nil
}

func flags(args []string, out io.Writer) (Config, map[string]bool, error) {
	c, e := defaults()
	if e != nil {
		return c, nil, e
	}
	f := flag.NewFlagSet("kora", flag.ContinueOnError)
	f.SetOutput(out)
	f.StringVar(&c.CatalogSource, "resources", c.CatalogSource, "resources.json local path or HTTP(S)/GitHub URL")
	f.BoolVar(&c.Yes, "yes", false, "Run unattended; accept the resolved installation plan")
	f.BoolVar(&c.Start, "start", false, "Start the installed server after unattended installation")
	f.StringVar(&c.DirectoryName, "name", "", "New installation directory name under --output; existing names fail")
	f.StringVar(&c.RecipeInput, "recipe", c.RecipeInput, "txAdmin YAML file, GitHub URL, or bundled; Git required")
	f.StringVar(&c.Server, "server", "", "Inspect an existing server directory in the TUI")
	f.StringVar(&c.WriteTemplate, "write-template", "", "Create a base resource template at this new path after TUI confirmation")
	f.BoolVar(&c.WindowsService, "windows-service", false, "Register FXServer as an automatic Windows service (requires elevation)")
	f.StringVar(&c.DBSetup, "db-setup", c.DBSetup, "Native database: new-service (MariaDB) or existing (MariaDB/MySQL)")
	f.StringVar(&c.MariaDBVersion, "mariadb-version", c.MariaDBVersion, "Official MariaDB Windows ZIP version for a new service")
	f.BoolVar(&c.Docker, "docker", c.Docker, "Use Docker on Linux; Windows uses native FXServer")
	f.BoolVar(&c.TXAdmin, "txadmin", c.TXAdmin, "Enable txAdmin")
	f.IntVar(&c.TXPort, "tx-port", c.TXPort, "txAdmin port")
	f.StringVar(&c.DBType, "db-type", c.DBType, "maria or mysql")
	f.BoolVar(&c.Adminer, "adminer", false, "Add Adminer, preconfigured for this database")
	f.IntVar(&c.AdminerPort, "adminer-port", c.AdminerPort, "Local Adminer web port (bound to 127.0.0.1)")
	f.BoolVar(&c.DBExposePort, "db-expose-port", false, "Bind the database port to the host")
	f.IntVar(&c.DBHostPort, "db-host-port", c.DBHostPort, "Host port for the database bind (default 3306)")
	f.BoolVar(&c.DBAuto, "db-auto", true, "Generate a new database name, user and password")
	f.StringVar(&c.DBName, "db-name", c.DBName, "New database name; existing databases are never reused")
	f.StringVar(&c.DBUser, "db-user", c.DBUser, "New database user; existing accounts are never reused")
	f.StringVar(&c.DBPassword, "db-password", c.DBPassword, "New database password (prefer masked wizard input)")
	f.StringVar(&c.DBHost, "db-host", c.DBHost, "Native database host")
	f.IntVar(&c.DBPort, "db-port", c.DBPort, "Native database port")
	f.StringVar(&c.DBAdmin, "db-admin", c.DBAdmin, "Native database account with CREATE DATABASE/USER/GRANT privileges")
	f.StringVar(&c.DBAdminPassword, "db-admin-password", "", "Native DB administrator password (prefer masked input; not saved)")
	f.StringVar(&c.CFXUser, "cfx-user", "", "Cfx.re username (informational, not an authentication token)")
	f.IntVar(&c.CFXPort, "cfx-port", c.CFXPort, "FiveM TCP/UDP port")
	f.StringVar(&c.Hostname, "hostname", c.Hostname, "Server display name (default local hostname)")
	f.StringVar(&c.License, "license-key", "", "Cfx.re license key (prefer masked wizard input)")
	f.StringVar(&c.GameBuild, "game-build", c.GameBuild, "FiveM Legacy game build number or latest")
	f.StringVar(&c.Artifact, "artifact", c.Artifact, "recommended or official runtime.fivem.net artifact URL")
	f.StringVar(&c.KoraTag, "kora-tag", c.KoraTag, "latest per-repository tag (default), default for branch heads, or exact tag")
	f.StringVar(&c.Output, "output", c.Output, "Parent directory; always creates a new installation folder")
	f.BoolVar(&c.Report, "report", false, "Write a redacted report")
	f.StringVar(&c.ReportPath, "report-path", c.ReportPath, "Report path relative to the NEW installation")
	f.BoolVar(&c.Secrets, "secrets", false, "Write a separate private secrets export")
	f.StringVar(&c.SecretsPath, "secrets-path", c.SecretsPath, "Secrets path relative to the NEW installation")
	f.BoolVar(&c.ArchiveFallback, "archive-fallback", false, "Consent to Go HTTPS archive downloads when Git is missing; loses Git history")
	f.StringVar(&c.Template, "template", "", "Optional local JSON template; adds resources and overrides matching paths")
	f.Lookup("db-password").DefValue = ""
	if e = f.Parse(args); e != nil {
		return c, nil, e
	}
	if c.License == "" {
		c.License = os.Getenv("CFX_LICENSE_KEY")
	}
	if f.NArg() != 0 {
		return c, nil, errors.New("unexpected positional arguments")
	}
	explicit := map[string]bool{}
	f.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if (explicit["recipe"] || explicit["template"] || c.Server != "") && !explicit["resources"] {
		c.CatalogSource = ""
	}
	if c.Server != "" && c.RecipeInput == "bundled" {
		c.RecipeInput = ""
	}
	if c.Template != "" && c.RecipeInput == "bundled" {
		c.RecipeInput = ""
	}
	if c.Template != "" && (strings.HasSuffix(strings.ToLower(c.Template), ".yaml") || strings.HasSuffix(strings.ToLower(c.Template), ".yml") || strings.HasPrefix(c.Template, "https://")) {
		if c.RecipeInput != "" {
			return c, nil, errors.New("use --template or --recipe, not both")
		}
		c.RecipeInput = c.Template
		c.Template = ""
	}
	if c.RecipeInput != "" && (c.Template != "" || c.Server != "" || c.ArchiveFallback) {
		return c, nil, errors.New("--recipe requires Git and cannot combine with --template, --server or --archive-fallback")
	}
	if c.Server != "" && (c.WriteTemplate != "" || c.Template != "") {
		return c, nil, errors.New("--server cannot be combined with --template or --write-template; templates customize new installations")
	}
	if runtime.GOOS != "windows" && !c.Docker {
		return c, nil, errors.New("native installation is for Windows; use Docker on Linux")
	}
	if runtime.GOOS == "windows" && c.Docker {
		return c, nil, errors.New("Windows installations are native; omit --docker")
	}
	seen := map[string]bool{}
	f.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if seen["db-name"] || seen["db-user"] || seen["db-password"] {
		c.DBAuto = false
		seen["db-auto"] = true
	}
	return c, seen, nil
}
func safeText(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 || r == '"' || r == '\\' {
			return false
		}
	}
	return true
}
func relativePath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\:\x00<>\"|?*") || filepath.IsAbs(p) {
		return false
	}
	for _, char := range p {
		if char < 32 || char == 127 {
			return false
		}
	}
	for _, v := range strings.Split(p, "/") {
		if v == "" || v == "." || v == ".." || strings.HasSuffix(v, ".") || strings.HasSuffix(v, " ") {
			return false
		}
		stem := strings.ToUpper(strings.Split(v, ".")[0])
		if regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$`).MatchString(stem) {
			return false
		}
	}
	return true
}
func (c Config) Validate() error {
	if c.DirectoryName != "" && (!relativePath(c.DirectoryName) || strings.Contains(c.DirectoryName, "/") || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(c.DirectoryName)) {
		return errors.New("directory name must be a single portable name using letters, digits, dots, underscores or hyphens")
	}

	if !c.Docker {
		if c.Adminer || c.DBExposePort {
			return errors.New("Adminer and Docker database port publishing are unavailable in native mode")
		}
		if c.DBSetup != "existing" && c.DBSetup != "new-service" {
			return errors.New("native db-setup must be existing or new-service")
		}
		if c.DBSetup == "new-service" && (c.DBType != "maria" || c.DBHost != "127.0.0.1") {
			return errors.New("new-service installs local MariaDB; use existing for MySQL or remote databases")
		}
		if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(c.MariaDBVersion) {
			return errors.New("MariaDB version must be major.minor.patch")
		}
	}
	if !c.Docker && c.DBSetup == "new-service" && (c.DBPort == c.CFXPort || (c.TXAdmin && c.DBPort == c.TXPort)) {
		return errors.New("new MariaDB service needs a port different from FiveM and txAdmin")
	}
	if c.Docker && c.WindowsService {
		return errors.New("windows-service is only available for native Windows installations")
	}
	if c.DBType != "maria" && c.DBType != "mysql" {
		return errors.New("Kora supports maria or mysql")
	}
	for _, p := range []int{c.TXPort, c.CFXPort, c.DBPort, c.DBHostPort, c.AdminerPort} {
		if p < 1 || p > 65535 {
			return errors.New("ports must be between 1 and 65535")
		}
	}
	if c.Adminer && (c.AdminerPort == c.CFXPort || (c.TXAdmin && c.AdminerPort == c.TXPort) || (c.DBExposePort && c.AdminerPort == c.DBHostPort)) {
		return errors.New("Adminer needs a different host port from FiveM, txAdmin and the published database")
	}
	if c.DBExposePort && (c.DBHostPort == c.CFXPort || (c.TXAdmin && c.DBHostPort == c.TXPort)) {
		return errors.New("database host port must differ from the FiveM and txAdmin ports")
	}
	if c.TXAdmin && c.TXPort == 30120 {
		return errors.New("txAdmin does not permit port 30120; use another port")
	}
	if c.TXAdmin && c.TXPort == c.CFXPort {
		return errors.New("FiveM and txAdmin need different ports")
	}
	if !identifier.MatchString(c.DBName) || !identifier.MatchString(c.DBUser) {
		return errors.New("database name/user: start with a letter; use up to 32 letters, digits or underscores")
	}
	if c.DBPassword == "" || c.License == "" || c.CFXUser == "" || c.Hostname == "" || c.Output == "" {
		return errors.New("database password, Cfx username, license, hostname and output are required")
	}
	for _, s := range []string{c.CFXUser, c.Hostname, c.License, c.DBHost, c.DBAdmin} {
		if !safeText(s) {
			return errors.New("text fields cannot contain quotes, backslashes or control characters")
		}
	}
	if strings.ContainsAny(c.DBPassword, "\x00\r\n") {
		return errors.New("database password cannot contain NUL or newlines")
	}
	if c.GameBuild != "latest" {
		v, e := strconv.Atoi(c.GameBuild)
		if e != nil || v < 1 {
			return errors.New("game build must be latest or a positive number")
		}
	}
	if c.KoraTag == "" || strings.HasPrefix(c.KoraTag, "-") || !safeText(c.KoraTag) {
		return errors.New("invalid resource tag")
	}
	paths := []string{}
	if c.Report {
		paths = append(paths, c.ReportPath)
	}
	if c.Secrets {
		paths = append(paths, c.SecretsPath)
	}
	for _, p := range paths {
		if !relativePath(p) || !strings.HasSuffix(strings.ToLower(p), ".json") {
			return errors.New("export paths must be .json files within the new installation")
		}
		top := strings.ToLower(strings.Split(p, "/")[0])
		switch top {
		case "catalog-source.json", "recipe-sources", "recipe-output", "recipe-lock.json", "resources", "resource-updates", "database-data", "installation.json", "base-template.json", ".env", ".gitignore", "server-data", "artifacts", "start.py", "compose.yaml", "dockerfile", ".compose-env", "runtime", "database-init":
			return fmt.Errorf("export path %s is reserved", p)
		}
	}
	if c.Report && c.Secrets && (strings.EqualFold(c.ReportPath, c.SecretsPath) || strings.HasPrefix(strings.ToLower(c.ReportPath)+"/", strings.ToLower(c.SecretsPath)+"/") || strings.HasPrefix(strings.ToLower(c.SecretsPath)+"/", strings.ToLower(c.ReportPath)+"/")) {
		return errors.New("report and secrets paths overlap")
	}
	return nil
}
func loadRecipe(path string) (Recipe, error) {
	var r Recipe
	if path == "" {
		return r, nil
	}
	f, e := os.Open(path)
	if e != nil {
		return r, e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1024*1024))
	d.DisallowUnknownFields()
	if e = d.Decode(&r); e != nil {
		return r, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return r, errors.New("template must contain one JSON object")
	}
	if r.Version != 1 {
		return r, errors.New("template version must be 1")
	}
	for _, s := range r.Ensure {
		if !resourceName.MatchString(s) || strings.HasPrefix(s, "-") {
			return r, errors.New("invalid template ensure name")
		}
	}
	return r, nil
}

func nativeDBDefault() string {
	if runtime.GOOS == "windows" {
		return "new-service"
	}
	return "existing"
}
