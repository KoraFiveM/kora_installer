package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ulikunitz/xz"
)

func validConfig(t *testing.T) Config {
	t.Helper()
	c, e := defaults()
	if e != nil {
		t.Fatal(e)
	}
	c.Docker = true // Shared fixtures exercise Docker consistently on every test host.
	c.License = "test-license-SENSITIVE"
	c.CFXUser = "test-user"
	c.DBAdminPassword = "admin-SENSITIVE"
	c.DBPassword = "db-SENSITIVE$'\\\""
	c.Output = t.TempDir()
	return c
}
func TestValidation(t *testing.T) {
	base := validConfig(t)
	if e := base.Validate(); e != nil {
		t.Fatal(e)
	}
	tests := map[string]func(*Config){"postgres": func(c *Config) { c.DBType = "postgres" }, "port collision": func(c *Config) { c.TXAdmin = true; c.TXPort = c.CFXPort }, "sql injection": func(c *Config) { c.DBName = "x`; DROP DATABASE x" }, "cfg injection": func(c *Config) { c.Hostname = "bad\nexec hack" }, "path traversal": func(c *Config) { c.Secrets = true; c.SecretsPath = "../secrets.json" }, "absolute export": func(c *Config) { c.Report = true; c.ReportPath = "C:/report.json" }, "reserved": func(c *Config) { c.Report = true; c.ReportPath = "server-data/report.json" }, "case collision": func(c *Config) { c.Report = true; c.Secrets = true; c.SecretsPath = "REPORT.json" }, "nested collision": func(c *Config) { c.Report = true; c.Secrets = true; c.SecretsPath = "report.json/secrets.json" }}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			c := base
			edit(&c)
			if e := c.Validate(); e == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}
func TestGeneratePrivateRedactedAndExclusive(t *testing.T) {
	c := validConfig(t)
	c.Report = true
	c.Secrets = true
	c.SecretsPath = "private/secrets.json"
	c.Docker = true
	p := Plan{Config: c}
	root := t.TempDir()
	if e := secureDir(root); e != nil {
		t.Fatal(e)
	}
	if e := generate(root, p); e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(filepath.Join(root, ".env"))
	if e != nil {
		t.Fatal(e)
	}
	if e = generate(root, p); e == nil {
		t.Fatal("second generation overwrote files")
	}
	current, _ := os.ReadFile(filepath.Join(root, ".env"))
	if !bytes.Equal(original, current) {
		t.Fatal("existing .env changed")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(root, ".env"))
		if info.Mode().Perm() != 0600 {
			t.Fatalf("env permissions: %o", info.Mode().Perm())
		}
	}
	report := jsonBytes(Report{Config: c})
	cfgBytes, _ := os.ReadFile(filepath.Join(root, "server-data/server.cfg"))
	for _, secret := range []string{c.License, c.DBPassword, c.DBAdminPassword} {
		if bytes.Contains(report, []byte(secret)) || bytes.Contains(cfgBytes, []byte(secret)) {
			t.Fatal("secret leaked into report/config")
		}
	}
	if bytes.Contains(original, []byte(c.DBAdminPassword)) {
		t.Fatal("admin password persisted")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(original)), "\n") {
		pair := strings.SplitN(line, "=", 2)
		var value string
		if e = json.Unmarshal([]byte(pair[1]), &value); e != nil {
			t.Fatal(e)
		}
		if pair[0] == "DB_PASSWORD" && value != c.DBPassword {
			t.Fatal("password did not round-trip")
		}
	}
	ignore, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if !bytes.Contains(ignore, []byte("/private/secrets.json")) {
		t.Fatal("custom export not ignored")
	}
}
func TestWriteNewRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink privileges vary")
	}
	root, outside := t.TempDir(), t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if e := writeNew(root, "escape/secrets.json", []byte("secret"), 0600); e == nil {
		t.Fatal("escaped through symlink")
	}
}
func zipFixture(t *testing.T, names []string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "test.zip")
	f, e := os.Create(file)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	for _, name := range names {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte("payload"))
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	return file
}
func TestZipTraversalAndDuplicate(t *testing.T) {
	for _, names := range [][]string{{"../escape"}, {"/absolute"}, {"repo/../../escape"}, {"repo/.git/config"}, {"repo/C:/escape"}, {"repo/NUL.txt"}, {"repo/a", "repo/a"}} {
		t.Run(strings.Join(names, "_"), func(t *testing.T) {
			if e := extractZip(zipFixture(t, names), t.TempDir(), true); e == nil {
				t.Fatal("unsafe zip accepted")
			}
		})
	}
	dest := t.TempDir()
	if e := extractZip(zipFixture(t, []string{"repo/fxmanifest.lua", "repo/sub/file.lua"}), dest, true); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dest, "fxmanifest.lua"))
	if e != nil || string(b) != "payload" {
		t.Fatal("valid archive failed")
	}
}
func TestTarLinksConfined(t *testing.T) {
	for _, target := range []string{"../../escape", "/etc/passwd", "lib.so"} {
		t.Run(target, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "a.tar.xz")
			f, _ := os.Create(file)
			x, _ := xz.NewWriter(f)
			w := tar.NewWriter(x)
			w.WriteHeader(&tar.Header{Name: "lib/link", Linkname: target, Typeflag: tar.TypeSymlink, Mode: 0777})
			w.Close()
			x.Close()
			f.Close()
			e := extractTarXZ(file, t.TempDir())
			if target != "lib.so" && e == nil {
				t.Fatal("escaping symlink accepted")
			}
			if target == "lib.so" && e != nil && runtime.GOOS != "windows" {
				t.Fatal(e)
			}
		})
	}
}
func TestManifestAndTemplate(t *testing.T) {
	list, e := resources(Recipe{})
	if e != nil || len(list) == 0 {
		t.Fatal(e)
	}
	override := Resource{Path: list[0].Path, Repository: "https://github.com/example/overridden.git", Ref: "v1"}
	extra := Resource{Path: "[custom]/extra", Repository: "https://github.com/example/extra.git"}
	merged, e := resources(Recipe{Resources: []Resource{override, extra}})
	if e != nil || merged[0].Repository != override.Repository || len(merged) != len(list)+1 {
		t.Fatal("merge failed", e)
	}
	for _, r := range []Resource{{Path: "../escape", Repository: extra.Repository}, {Path: list[0].Path + "/overlap", Repository: extra.Repository}, {Path: "[custom]/x", Repository: "https://user:password@github.com/a/b"}} {
		if _, e = resources(Recipe{Resources: []Resource{r}}); e == nil {
			t.Fatal("unsafe recipe accepted")
		}
	}
}
func TestBuildParser(t *testing.T) {
	builds, e := parseBuilds([]byte("**FiveM builds**\n| 1 | Base |\n| 3407 | a |\n| 3889 | b |\n**RedM builds**\n| 99999 | nope |"))
	if e != nil || len(builds) != 2 || builds[0] != 3889 {
		t.Fatal(builds, e)
	}
	if _, e = parseBuilds([]byte("format changed")); e == nil {
		t.Fatal("silently accepted changed upstream format")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTagResolution(t *testing.T) {
	for _, tags := range []string{`[{"name":"v2"}]`, `[]`} {
		t.Run(tags, func(t *testing.T) {
			calls := []string{}
			s := Sources{API: "https://fixture", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				body := ""
				switch {
				case strings.HasSuffix(r.URL.Path, "/tags"):
					body = tags
				case strings.Contains(r.URL.Path, "/commits/"):
					body = `{"sha":"` + strings.Repeat("a", 40) + `"}`
				default:
					body = `{"default_branch":"main"}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			r, e := s.resolve(context.Background(), Resource{Repository: "https://github.com/KoraFiveM/kora_core.git"}, "latest")
			if e != nil {
				t.Fatal(e)
			}
			want := "v2"
			if tags == "[]" {
				want = "main"
			}
			if r.Ref != want || len(r.Commit) != 40 {
				t.Fatal(r, calls)
			}
		})
	}
}
func TestFlagsAndWizard(t *testing.T) {
	c, seen, e := flags([]string{"--docker=true", "--txadmin=false", "--report=false", "--db-name=fresh", "--license-key=secret"}, io.Discard)
	if e != nil || !seen["docker"] || !seen["txadmin"] || c.DBAuto {
		t.Fatal("explicit flags lost", e)
	}
	m := newModel(c, seen)
	m.step = 0
	m.next()
	if fields[m.step].Key != "resources" {
		t.Fatal("did not skip explicitly supplied/irrelevant fields", fields[m.step].Key)
	}
	m.config = validConfig(t)
	m.stage = "review"
	m.height = 60
	text := m.View().Content
	for _, secret := range []string{m.config.License, m.config.DBPassword, m.config.DBAdminPassword} {
		if strings.Contains(text, secret) {
			t.Fatal("summary leaks secrets")
		}
	}
	m.stage = "form"
	for i, f := range fields {
		if f.Key == "license-key" {
			m.step = i
		}
	}
	m.loadField()
	text = m.View().Content
	if strings.Contains(text, m.config.License) {
		t.Fatal("input leaks license")
	}
	for _, size := range [][2]int{{40, 15}, {80, 24}, {120, 40}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(model)
		if m.View().Content == "" {
			t.Fatal("empty view")
		}
	}
}
func TestArtifactPlatformValidation(t *testing.T) {
	for _, platform := range []string{"linux", "windows"} {
		path := "build_proot_linux/master/123-abc/fx.tar.xz"
		if platform == "windows" {
			path = "build_server_windows/master/123-abc/server.zip"
		}
		if e := artifactURL("https://runtime.fivem.net/artifacts/fivem/"+path, platform); e != nil {
			t.Fatal(e)
		}
	}
	for _, address := range []string{"http://runtime.fivem.net/artifacts/fivem/build_proot_linux/master/1/fx.tar.xz", "https://evil.example/fx.tar.xz", "https://runtime.fivem.net/artifacts/fivem/build_server_windows/master/1/server.zip"} {
		if artifactURL(address, "linux") == nil {
			t.Fatal(fmt.Sprintf("accepted %s", address))
		}
	}
}

func TestComposeConfiguration(t *testing.T) {
	docker, e := exec.LookPath("docker")
	if e != nil {
		t.Skip("Docker CLI unavailable")
	}
	for _, db := range []string{"maria", "mysql"} {
		for _, expose := range []bool{false, true} {
			for _, adminer := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/publish=%t/adminer=%t", db, expose, adminer), func(t *testing.T) {
					c := validConfig(t)
					c.Docker = true
					c.TXAdmin = true
					c.DBType = db
					c.DBExposePort = expose
					c.DBHostPort = 3307
					c.Adminer = adminer
					c.AdminerPort = 18080
					root := t.TempDir()
					if err := generate(root, Plan{Config: c}); err != nil {
						t.Fatal(err)
					}
					cmd := exec.Command(docker, "compose", "--env-file", ".compose-env", "config", "--format", "json")
					cmd.Dir = root
					cmd.Env = append(os.Environ(), "DB_NAME=fixture", "DB_USER=fixture", "DB_PASSWORD=placeholder", "DB_ROOT_PASSWORD=placeholder", "DB_HOST_PORT=3307", "ADMINER_PORT=18080")
					out, err := cmd.Output()
					if err != nil {
						t.Fatalf("Compose validation: %v", err)
					}
					var config struct {
						Services map[string]struct {
							Ports []struct {
								Published string
								Target    int
								HostIP    string `json:"host_ip"`
							}
							Environment map[string]string
							Volumes     []struct{ Source, Target, Type string }
							DependsOn   map[string]struct{ Condition string } `json:"depends_on"`
						}
					}
					if err = json.Unmarshal(out, &config); err != nil {
						t.Fatal(err)
					}
					ports := config.Services["db"].Ports
					if expose {
						if len(ports) != 1 || ports[0].Published != "3307" || ports[0].Target != 3306 {
							t.Fatalf("wrong database bind: %+v", ports)
						}
					} else if len(ports) != 0 {
						t.Fatal("published private database")
					}
					service, present := config.Services["adminer"]
					if present != adminer {
						t.Fatal("wrong optional Adminer service state")
					}
					if adminer {
						if service.Environment["ADMINER_DEFAULT_SERVER"] != "db" || service.DependsOn["db"].Condition != "service_healthy" {
							t.Fatal("Adminer is not wired to the healthy database")
						}
						if len(service.Ports) != 1 || service.Ports[0].HostIP != "127.0.0.1" || service.Ports[0].Published != "18080" || service.Ports[0].Target != 8080 {
							t.Fatalf("wrong Adminer bind: %+v", service.Ports)
						}
						if len(service.Environment) != 1 {
							t.Fatal("unexpected credentials in Adminer environment")
						}
					}
					fivem := config.Services["fivem"]
					if fivem.DependsOn["db"].Condition != "service_healthy" {
						t.Fatal("FXServer does not wait for DB health")
					}
					bound := false
					for _, v := range fivem.Volumes {
						if v.Target == "/srv/kora/server-data/resources" && v.Type == "bind" && filepath.Base(v.Source) == "resources" {
							bound = true
						}
					}
					if !bound {
						t.Fatal("missing live resource bind mount")
					}
				})
			}
		}
	}
}

func TestDockerArchiveRejectsSymlinkParents(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		file := filepath.Join(t.TempDir(), "fx.tar.xz")
		f, _ := os.Create(file)
		x, _ := xz.NewWriter(f)
		w := tar.NewWriter(x)
		w.WriteHeader(&tar.Header{Name: "run.sh", Typeflag: tar.TypeReg, Mode: 0755, Size: 1})
		w.Write([]byte("x"))
		w.WriteHeader(&tar.Header{Name: "lib", Typeflag: tar.TypeSymlink, Linkname: "safe", Mode: 0777})
		if unsafe {
			w.WriteHeader(&tar.Header{Name: "lib/payload", Typeflag: tar.TypeReg, Mode: 0600, Size: 1})
			w.Write([]byte("x"))
		}
		w.Close()
		x.Close()
		f.Close()
		if e := validateTarXZ(file); (e != nil) != unsafe {
			t.Fatalf("unsafe=%t got %v", unsafe, e)
		}
	}
}

func TestOfficialDockerArtifactFixture(t *testing.T) {
	file := os.Getenv("KORA_ARTIFACT_FIXTURE")
	if file == "" {
		t.Skip("set KORA_ARTIFACT_FIXTURE to check an already downloaded FXServer archive")
	}
	if err := validateTarXZ(file); err != nil {
		t.Fatalf("official artifact rejected: %v", err)
	}
}
