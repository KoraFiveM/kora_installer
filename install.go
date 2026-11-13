package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/start.py
var launcher string

type Plan struct {
	TXRecipe    *TXRecipe
	Config      Config
	Resources   []Resource
	Ensure      []string
	ArtifactURL string
	Platform    string
	Git         bool
}
type Progress struct {
	Text  string
	Done  int
	Total int
}
type Result struct {
	Folder string
	Err    error
}
type Report struct {
	Version     int        `json:"version"`
	Created     string     `json:"created"`
	Config      Config     `json:"configuration"`
	Resources   []Resource `json:"resources"`
	ArtifactURL string     `json:"artifact_url"`
	GitTracking bool       `json:"git_tracking"`
	Status      string     `json:"status"`
}

func targetPlatform(c Config) string {
	if c.Docker {
		return "linux"
	}
	return runtime.GOOS
}
func prepare(ctx context.Context, c Config, progress func(Progress)) (Plan, error) {
	p := Plan{Config: c, Platform: targetPlatform(c)}
	if !c.Docker && (c.WindowsService || c.DBSetup == "new-service") {
		if err := nativePrivilegeCheck(); err != nil {
			return p, err
		}
	}
	if e := c.Validate(); e != nil {
		return p, e
	}
	if p.Platform != "linux" && p.Platform != "windows" {
		return p, errors.New("native installs support Windows and Linux")
	}
	if runtime.GOARCH != "amd64" {
		return p, errors.New("FiveM server artifacts require an x86-64 host")
	}
	r, e := loadRecipe(c.Template)
	if e != nil {
		return p, e
	}
	list, e := resources(r)
	if e != nil {
		return p, e
	}
	if c.RecipeInput != "" {
		var recipeResources []Resource
		p.TXRecipe, recipeResources, e = loadTXRecipe(ctx, c.RecipeInput)
		if e != nil {
			return p, e
		}
		list, e = resources(Recipe{Version: 1, Resources: recipeResources, Replace: true})
		if e != nil {
			return p, e
		}
	}
	if c.CatalogSource != "" {
		progress(Progress{Text: "Loading resources.json catalog"})
		catalog, source, err := loadCatalog(ctx, newSources().Client, c.CatalogSource, defaultCatalogURL, fallbackCatalogURL)
		if err != nil {
			return p, err
		}
		list, err = catalogResources(catalog, list, c.KoraTag, c.CatalogSource == defaultCatalogURL)
		if err != nil {
			return p, err
		}
		if !strings.HasPrefix(c.CatalogSource, "http://") && !strings.HasPrefix(c.CatalogSource, "https://") {
			p.Config.CatalogSource, _ = filepath.Abs(c.CatalogSource)
		}
		progress(Progress{Text: "CATALOG " + source})
	}
	p.Ensure = r.Ensure
	if _, e = exec.LookPath("git"); e == nil {
		p.Git = true
	} else if !c.ArchiveFallback || p.TXRecipe != nil {
		return p, errors.New("Git was not found; install Git (recommended), or explicitly confirm archive fallback")
	}
	s := newSources()
	progress(Progress{"Resolving FiveM game build and FXServer artifact", 0, len(list) + 1})
	if c.GameBuild == "latest" {
		builds, e := s.builds(ctx)
		if e != nil {
			return p, e
		}
		p.Config.GameBuild = strconv.Itoa(builds[0])
	}
	p.ArtifactURL, e = s.artifact(ctx, c.Artifact, p.Platform)
	if e != nil {
		return p, e
	}
	progress(Progress{"GAME BUILD " + p.Config.GameBuild + "  /  FXSERVER " + p.ArtifactURL, 1, len(list) + 1})
	for i, r := range list {
		progress(Progress{"LOOKUP " + r.Path + " @ " + c.KoraTag, i + 1, len(list) + 1})
		tag := c.KoraTag
		if p.TXRecipe != nil && r.Commit == "" {
			tag = "default"
			if c.KoraTag != "recipe" && strings.HasPrefix(strings.ToLower(r.Repository), "https://github.com/korafivem/kora_") {
				r.Ref = ""
				tag = c.KoraTag
			}
		}
		resolved := r
		var e error
		if r.Commit == "" {
			resolved, e = s.resolve(ctx, r, tag)
		}
		if e != nil {
			return p, fmt.Errorf("%s: %w", r.Path, e)
		}
		if resolved.SelectionNote != "" {
			progress(Progress{Text: "VERSION FALLBACK " + r.Path + ": " + resolved.SelectionNote})
		}
		progress(Progress{"PINNED " + r.Path + " @ " + resolved.Ref + "  " + resolved.Commit[:12], i + 1, len(list) + 1})
		p.Resources = append(p.Resources, resolved)
	}
	if p.TXRecipe != nil {
		for i, source := range p.TXRecipe.Sources {
			resolved, err := s.resolve(ctx, source, "default")
			if err != nil {
				return p, err
			}
			p.TXRecipe.Sources[i] = resolved
		}
	}
	return p, nil
}
func writeNew(root, name string, data []byte, mode os.FileMode) error {
	if !relativePath(name) {
		return errors.New("unsafe output path")
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return e
	}
	defer r.Close()
	if e = r.MkdirAll(filepath.Dir(name), 0700); e != nil {
		return e
	}
	f, e := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func jsonBytes(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return append(b, '\n') }
func cfg(c Config, ensure []string) string {
	s := fmt.Sprintf("# Kora Framework development server. Secrets are injected by start.py.\nendpoint_add_tcp \"0.0.0.0:%d\"\nendpoint_add_udp \"0.0.0.0:%d\"\nsv_hostname \"%s\"\nsets sv_projectName \"Kora\"\nsets sv_projectDesc \"Kora development server\"\nsv_enforceGameBuild %s\nsv_maxclients 48\nset steam_webApiKey \"none\"\nset resources_useSystemChat true\nset chat_showJoins false\nset chat_showQuits false\nensure mapmanager\nensure kora_lib\nensure kora_sql\nensure kora_inventory\nensure kora_core\nensure [compat]\nensure [kora]\nensure [standalone]\nensure [default-maps]\nadd_ace resource.kora_core command allow\n", c.CFXPort, c.CFXPort, c.Hostname, c.GameBuild)
	for _, name := range ensure {
		s += "ensure " + name + "\n"
	}
	return s
}
func environment(c Config) (map[string]string, error) {
	rootPass, e := randomID(24)
	if e != nil {
		return nil, e
	}
	host := c.DBHost
	port := c.DBPort
	if c.Docker {
		host = "db"
		port = 3306
	}
	return map[string]string{"ADMINER_ENABLED": strconv.FormatBool(c.Adminer), "ADMINER_PORT": strconv.Itoa(c.AdminerPort), "DB_TYPE": c.DBType, "DB_HOST": host, "DB_PORT": strconv.Itoa(port), "DB_NAME": c.DBName, "DB_USER": c.DBUser, "DB_PASSWORD": c.DBPassword, "DB_ROOT_PASSWORD": rootPass, "DB_EXPOSE_PORT": strconv.FormatBool(c.DBExposePort), "DB_HOST_PORT": strconv.Itoa(c.DBHostPort), "CFX_LICENSE_KEY": c.License, "CFX_USERNAME": c.CFXUser, "CFX_PORT": strconv.Itoa(c.CFXPort), "TXADMIN_ENABLED": strconv.FormatBool(c.TXAdmin), "TXADMIN_PORT": strconv.Itoa(c.TXPort), "DOCKER": strconv.FormatBool(c.Docker)}, nil
}
func compose(c Config, project string) string {
	image, prefix, data := "mariadb:11.4", "MARIADB", "/var/lib/mysql"
	health := `["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]`
	if c.DBType == "mysql" {
		image, prefix = "mysql:8.4", "MYSQL"
		health = `["CMD", "mysqladmin", "ping", "--host=127.0.0.1", "--silent"]`
	}
	tx := ""
	dbPort := ""
	if c.DBExposePort {
		dbPort = "    ports:\n      - '${DB_HOST_PORT:-3306}:3306/tcp'\n"
	}
	if c.TXAdmin {
		tx = fmt.Sprintf("      - '%d:%d/tcp'\n", c.TXPort, c.TXPort)
	}
	adminer := ""
	if c.Adminer {
		adminer = `  adminer:
    image: adminer:6.3.0
    environment:
      ADMINER_DEFAULT_SERVER: db
    depends_on:
      db:
        condition: service_healthy
    ports:
      - '127.0.0.1:${ADMINER_PORT:-8080}:8080/tcp'
`
	}
	return fmt.Sprintf(`name: %s
services:
  db:
    image: %s
    command: ['--character-set-server=utf8mb4', '--collation-server=utf8mb4_unicode_ci']
    environment:
      %s_DATABASE: ${DB_NAME:?}
      %s_USER: ${DB_USER:?}
      %s_PASSWORD: ${DB_PASSWORD:?}
      %s_ROOT_PASSWORD: ${DB_ROOT_PASSWORD:?}
%s    volumes:
      - ./database-data:%s
    healthcheck:
      test: %s
      interval: 5s
      timeout: 5s
      retries: 24
      start_period: 30s
  fivem:
    build: .
    depends_on:
      db:
        condition: service_healthy
    init: true
    stdin_open: true
    tty: true
    ports:
      - '%d:%d/tcp'
      - '%d:%d/udp'
%s    volumes:
      - ./:/srv/kora
      - ./resources:/srv/kora/server-data/resources
    working_dir: /srv/kora
    command: ['python3', 'start.py', '--inside-container']
%s`, project, image, prefix, prefix, prefix, prefix, dbPort, data, health, c.CFXPort, c.CFXPort, c.CFXPort, c.CFXPort, tx, adminer)
}

const dockerfile = `FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates python3 libatomic1 xz-utils && rm -rf /var/lib/apt/lists/*
COPY install.complete /tmp/install.complete
COPY artifact.download /tmp/fx.tar.xz
RUN mkdir -p /opt/fxserver && tar -xJf /tmp/fx.tar.xz -C /opt/fxserver --no-same-owner && test -f /opt/fxserver/run.sh && rm /tmp/fx.tar.xz
WORKDIR /srv/kora
`

func generate(root string, p Plan) error {
	c := p.Config
	if e := os.MkdirAll(nativeResources(root, c), 0700); e != nil {
		return e
	}
	env, e := environment(c)
	if e != nil {
		return e
	}
	var dotenv strings.Builder
	// JSON string values form a deliberately restricted dotenv dialect, parsed
	// without shell evaluation by start.py. Compose receives the decoded environment.
	keys := []string{"DOCKER", "ADMINER_ENABLED", "ADMINER_PORT", "DB_TYPE", "DB_HOST", "DB_PORT", "DB_EXPOSE_PORT", "DB_HOST_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_ROOT_PASSWORD", "CFX_LICENSE_KEY", "CFX_USERNAME", "CFX_PORT", "TXADMIN_ENABLED", "TXADMIN_PORT"}
	for _, k := range keys {
		b, _ := json.Marshal(env[k])
		fmt.Fprintf(&dotenv, "%s=%s\n", k, b)
	}
	files := map[string]string{".env": dotenv.String(), ".compose-env": "", "start.py": launcher, "server-data/server.cfg": cfg(c, p.Ensure), ".gitignore": ".env\ndatabase-admin.json\n/database/\n/kora.exe\nnative.json\nreport.json\nsecrets.json\n/runtime/\n/database-data/\n/artifacts/\n/artifact.download\n/resource-*.zip\n/resource-updates/\n"}
	if p.TXRecipe != nil {
		delete(files, "server-data/server.cfg")
	}
	if c.Report {
		files[".gitignore"] += "/" + escapeIgnore(c.ReportPath) + "\n"
	}
	if c.Secrets {
		files[".gitignore"] += "/" + escapeIgnore(c.SecretsPath) + "\n"
	}
	if c.Docker {
		id, err := randomID(8)
		if err != nil {
			return err
		}
		project := "kora-" + id
		files["compose.yaml"] = compose(c, project)
		files["Dockerfile"] = dockerfile
		files[".dockerignore"] = "**\n!Dockerfile\n!artifact.download\n!install.complete\n"
	}
	for n, v := range files {
		if e = writeNew(root, n, []byte(v), 0600); e != nil {
			return e
		}
	}
	if c.Secrets {
		if e = writeNew(root, c.SecretsPath, jsonBytes(env), 0600); e != nil {
			return e
		}
	}
	return nil
}
func escapeIgnore(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "?", "\\?", "!", "\\!", "#", "\\#", " ", "\\ ")
	return r.Replace(s)
}
func install(ctx context.Context, p Plan, progress func(Progress)) (result Result) {
	c := p.Config
	if !c.Docker && runtime.GOOS != "windows" {
		return Result{Err: errors.New("native installs require Windows; use Docker on Linux")}
	}
	if !c.Docker && (c.WindowsService || c.DBSetup == "new-service") {
		if err := nativePrivilegeCheck(); err != nil {
			return Result{Err: err}
		}
	}
	if e := c.Validate(); e != nil {
		return Result{Err: e}
	}
	if e := os.MkdirAll(c.Output, 0700); e != nil {
		return Result{Err: e}
	}
	var root string
	var e error
	if c.DirectoryName != "" {
		root = filepath.Join(c.Output, c.DirectoryName)
		e = os.Mkdir(root, 0700)
	} else {
		root, e = os.MkdirTemp(c.Output, "kora-")
	}
	if e != nil {
		return Result{Err: e}
	}
	root, e = filepath.Abs(root)
	if e != nil {
		return Result{Err: e}
	}
	result.Folder = root
	fail := func(e error) Result { return Result{root, e} }
	if e = secureDir(root); e != nil {
		return fail(e)
	}
	if e = generate(root, p); e != nil {
		return fail(e)
	}
	total := len(p.Resources) + 3
	progress(Progress{"Downloading FXServer artifact", 0, total})
	artifact := filepath.Join(root, "artifact.download")
	if e = download(ctx, p.ArtifactURL, artifact); e != nil {
		return fail(e)
	}
	artifacts := filepath.Join(root, "artifacts")
	if e = os.Mkdir(artifacts, 0700); e != nil {
		return fail(e)
	}
	progress(Progress{"Extracting FXServer artifact", 1, total})
	emit := func(name string) { progress(Progress{"EXTRACT  " + name, 1, total}) }
	if c.Docker {
		e = validateTarXZProgress(artifact, emit)
	} else if p.Platform == "windows" {
		e = extractWindowsArtifact(artifact, artifacts, emit)
	} else {
		e = extractTarXZProgress(artifact, artifacts, emit)
	}
	if e != nil {
		return fail(e)
	}
	executable := "run.sh"
	if p.Platform == "windows" {
		executable = "FXServer.exe"
	}
	if _, e = os.Stat(filepath.Join(artifacts, executable)); !c.Docker && e != nil {
		return fail(errors.New("artifact is missing the server executable"))
	}
	// The download is retained: all installation output remains inspectable.
	for i, r := range p.Resources {
		if e = ctx.Err(); e != nil {
			return fail(e)
		}
		progress(Progress{"Installing " + r.Path, i + 2, total})
		dest := filepath.Join(nativeResources(root, c), filepath.FromSlash(r.Path))
		if e = prepareCloneDestination(root, dest); e != nil {
			return fail(e)
		}
		if p.Git {
			progress(Progress{"GIT CLONE  " + r.Path + " @ " + r.Ref, i + 2, total})
			cmd := exec.CommandContext(ctx, "git", "clone", "--no-checkout", "--", r.Repository, dest)
			cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
			if e = cmd.Run(); e != nil {
				return fail(fmt.Errorf("clone failed for %s; check Git credentials (partial checkout retained)", r.Path))
			}
			progress(Progress{"GIT CHECKOUT  " + r.Path + "  " + r.Commit[:12], i + 2, total})
			cmd = exec.CommandContext(ctx, "git", "-C", dest, "checkout", "--detach", r.Commit)
			if e = cmd.Run(); e != nil {
				if _, fetchErr := gitOutput(ctx, dest, "fetch", "--no-recurse-submodules", "origin", r.Commit); fetchErr != nil {
					return fail(fmt.Errorf("pinned commit unavailable for %s", r.Path))
				}
				if _, checkoutErr := gitOutput(ctx, dest, "checkout", "--detach", r.Commit); checkoutErr != nil {
					return fail(fmt.Errorf("checkout failed for %s", r.Path))
				}
			}
		} else {
			if e = os.Mkdir(dest, 0700); e != nil {
				return fail(e)
			}
			slug, _ := repoSlug(r.Repository)
			archive := filepath.Join(root, fmt.Sprintf("resource-%03d.zip", i))
			if e = download(ctx, "https://api.github.com/repos/"+slug+"/zipball/"+r.Commit, archive); e != nil {
				return fail(e)
			}
			if e = extractZipProgress(archive, dest, true, func(name string) { progress(Progress{"EXTRACT " + r.Path + "/" + name, i + 2, total}) }); e != nil {
				return fail(e)
			}
		}
	}
	if err := applyTXRecipe(ctx, root, p, progress); err != nil {
		return fail(err)
	}
	progress(Progress{"Creating isolated database", total - 1, total})
	var native nativeInstallation
	if !c.Docker {
		native, e = installNativeFiles(root, c)
		if e != nil {
			return fail(e)
		}
		if c.DBSetup == "new-service" {
			if e = installMariaDB(ctx, root, &c, native, progress); e != nil {
				return fail(e)
			}
		}
		if e = waitDatabase(ctx, c); e != nil {
			return fail(e)
		}
		if e = createDatabase(ctx, c); e != nil {
			return fail(e)
		}
	} else {
		if e = os.Mkdir(filepath.Join(root, "database-data"), 0700); e != nil {
			return fail(e)
		}
	}
	if c.Report {
		report := Report{1, time.Now().UTC().Format(time.RFC3339), c, p.Resources, p.ArtifactURL, p.Git, "installed; server not started"}
		if e = writeNew(root, c.ReportPath, jsonBytes(report), 0600); e != nil {
			return fail(e)
		}
	}
	if e = writeNew(root, "installation.json", jsonBytes(Recipe{Version: 1, Resources: p.Resources, Ensure: p.Ensure}), 0600); e != nil {
		return fail(e)
	}
	if e = writeNew(root, "base-template.json", jsonBytes(templateFor(p.Resources, p.Ensure)), 0600); e != nil {
		return fail(e)
	}
	if c.CatalogSource != "" {
		if e = writeNew(root, "catalog-source.json", jsonBytes(struct {
			Source string `json:"source"`
		}{c.CatalogSource}), 0600); e != nil {
			return fail(e)
		}
	}
	if !c.Docker {
		if e = registerFXService(root, native); e != nil {
			return fail(e)
		}
	}
	if e = writeNew(root, "install.complete", []byte("Kora installation complete\n"), 0600); e != nil {
		return fail(e)
	}
	if err := registerServer(root); err != nil {
		progress(Progress{Text: "Server installed; discovery registration failed. Use kora --server=" + root})
	}
	progress(Progress{"Installation complete", total, total})
	return result
}

func prepareCloneDestination(root, dest string) error {
	rel, err := filepath.Rel(root, dest)
	if err != nil || !relativePath(filepath.ToSlash(rel)) {
		return errors.New("unsafe clone destination")
	}
	current := root
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err = os.Mkdir(current, 0700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("clone parent is not a real directory")
		}
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return fmt.Errorf("clone destination already exists or is inaccessible: %s", dest)
	}
	return nil
}
