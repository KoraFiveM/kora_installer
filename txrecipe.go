package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

//go:embed assets/txadmin-recipe.yaml
var bundledTXRecipe []byte

type TXTask struct {
	Action  string    `yaml:"action"`
	Src     string    `yaml:"src"`
	Ref     string    `yaml:"ref"`
	Dest    string    `yaml:"dest"`
	Path    string    `yaml:"path"`
	File    yaml.Node `yaml:"file"`
	Search  string    `yaml:"search"`
	Replace string    `yaml:"replace"`
	Mode    string    `yaml:"mode"`
	Seconds int       `yaml:"seconds"`
}
type TXRecipe struct {
	Engine      int        `yaml:"$engine"`
	OneSync     string     `yaml:"$onesync"`
	Name        string     `yaml:"name"`
	Version     string     `yaml:"version"`
	Author      string     `yaml:"author"`
	Description string     `yaml:"description"`
	Tasks       []TXTask   `yaml:"tasks"`
	Sources     []Resource `yaml:"-"`
}

func recipePath(s string) (string, error) {
	s = strings.TrimPrefix(s, "./")
	if !relativePath(s) {
		return "", fmt.Errorf("unsafe recipe path %q", s)
	}
	for _, part := range strings.Split(s, "/") {
		if strings.EqualFold(part, ".git") {
			return "", errors.New("recipe cannot access Git metadata")
		}
	}
	return s, nil
}
func recipeFiles(t TXTask) ([]string, error) {
	var files []string
	if t.File.Kind == yaml.ScalarNode {
		files = []string{t.File.Value}
	} else if t.File.Kind == yaml.SequenceNode {
		if err := t.File.Decode(&files); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("recipe file must be a path or list of paths")
	}
	if len(files) == 0 {
		return nil, errors.New("empty recipe file list")
	}
	for i, f := range files {
		p, err := recipePath(f)
		if err != nil {
			return nil, err
		}
		files[i] = p
	}
	return files, nil
}
func parseTXRecipe(data []byte) (*TXRecipe, []Resource, error) {
	r := new(TXRecipe)
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(r); err != nil {
		return nil, nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, nil, errors.New("recipe must contain one YAML document")
	}
	if r.Engine != 3 || (r.OneSync != "on" && r.OneSync != "") {
		return nil, nil, errors.New("only txAdmin engine 3 with OneSync on is supported")
	}
	if len(r.Tasks) == 0 || len(r.Tasks) > 1000 {
		return nil, nil, errors.New("recipe must contain 1–1000 tasks")
	}
	var list []Resource
	var destinations []string
	for i, t := range r.Tasks {
		fail := func(err error) (*TXRecipe, []Resource, error) {
			return nil, nil, fmt.Errorf("recipe task %d (%s): %w", i+1, t.Action, err)
		}
		switch t.Action {
		case "download_github":
			dest, err := recipePath(t.Dest)
			if err != nil {
				return fail(err)
			}
			if _, err = repoSlug(t.Src); err != nil {
				return fail(err)
			}
			if !safeText(t.Ref) || strings.HasPrefix(t.Ref, "-") {
				return fail(errors.New("invalid ref"))
			}
			for _, prior := range destinations {
				a, b := strings.ToLower(dest), strings.ToLower(prior)
				if a == b {
					return fail(errors.New("overlapping clone destinations"))
				}
			}
			destinations = append(destinations, dest)
			resource := Resource{Path: strings.TrimPrefix(dest, "resources/"), Repository: t.Src, Ref: t.Ref}
			if strings.EqualFold(strings.TrimSuffix(t.Src, ".git"), "https://github.com/KoraFiveM/PolyZone") {
				resource.Repository = "https://github.com/mkafrin/PolyZone.git"
				if resource.Ref == "main" {
					resource.Ref = "master"
				}
			}
			if strings.HasPrefix(dest, "resources/") {
				list = append(list, resource)
			} else if strings.HasPrefix(dest, "tmp/") {
				resource.Path = dest
				r.Sources = append(r.Sources, resource)
			} else {
				return fail(errors.New("clones must target resources/ or tmp/"))
			}
		case "move_path":
			if _, err := recipePath(t.Src); err != nil {
				return fail(err)
			}
			dest, err := recipePath(t.Dest)
			if err != nil {
				return fail(err)
			}
			if strings.Contains(dest, "/") || (!strings.HasSuffix(dest, ".cfg") && !strings.HasSuffix(dest, ".sql")) {
				return fail(errors.New("only top-level cfg/sql file moves are supported"))
			}
		case "replace_string":
			if _, err := recipeFiles(t); err != nil {
				return fail(err)
			}
			if t.Mode != "" && t.Mode != "all_vars" {
				return fail(errors.New("unsupported replacement mode"))
			}
			if t.Mode == "" && t.Search == "" {
				return fail(errors.New("empty replacement search"))
			}
		case "query_database":
			if _, err := recipeFiles(t); err != nil {
				return fail(err)
			}
		case "connect_database", "waste_time":
		case "remove_path":
			path, err := recipePath(t.Path)
			if err != nil || path != "tmp" {
				return fail(errors.New("only tmp cleanup is supported; source checkouts are retained"))
			}
		default:
			return fail(errors.New("unsupported action; no shell commands, archive downloads, or destructive operations are run"))
		}
	}
	if len(list) == 0 {
		return nil, nil, errors.New("recipe has no resource repositories")
	}
	return r, list, nil
}
func loadTXRecipe(ctx context.Context, source string) (*TXRecipe, []Resource, error) {
	var data []byte
	var err error
	if source == "bundled" {
		data = bundledTXRecipe
	} else if strings.HasPrefix(source, "https://") {
		address := source
		if strings.HasPrefix(address, "https://github.com/") {
			address = strings.Replace(address, "https://github.com/", "https://raw.githubusercontent.com/", 1)
			address = strings.Replace(address, "/blob/", "/", 1)
		}
		if !strings.HasPrefix(address, "https://raw.githubusercontent.com/") {
			return nil, nil, errors.New("remote recipes must use GitHub blob or raw URLs")
		}
		data, err = newSources().get(ctx, address, 1<<20)
	} else {
		f, e := os.Open(source)
		if e != nil {
			return nil, nil, e
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, (1<<20)+1))
	}
	if err != nil {
		return nil, nil, err
	}
	if len(data) > 1<<20 {
		return nil, nil, errors.New("recipe exceeds 1 MiB")
	}
	return parseTXRecipe(data)
}

// Recipe file transformations happen in memory. Git checkouts remain untouched.
func applyTXRecipe(ctx context.Context, root string, p Plan, progress func(Progress)) error {
	r := p.TXRecipe
	if r == nil {
		return nil
	}
	files := map[string][]byte{}
	for i, source := range r.Sources {
		dest := filepath.Join(root, "recipe-sources", fmt.Sprintf("source-%03d", i))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		progress(Progress{Text: "GIT CLONE recipe source " + source.Repository})
		if _, err := gitOutput(ctx, root, "clone", "--no-checkout", "--", source.Repository, dest); err != nil {
			return errors.New("recipe source clone failed; check Git credentials")
		}
		if _, err := gitOutput(ctx, dest, "checkout", "--detach", source.Commit); err != nil {
			return err
		}
		sourceRoot, err := os.OpenRoot(dest)
		if err != nil {
			return err
		}
		for _, task := range r.Tasks {
			if task.Action != "move_path" {
				continue
			}
			src, _ := recipePath(task.Src)
			if !strings.HasPrefix(src, source.Path+"/") {
				continue
			}
			f, err := sourceRoot.Open(strings.TrimPrefix(src, source.Path+"/"))
			if err != nil {
				sourceRoot.Close()
				return err
			}
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() {
				f.Close()
				sourceRoot.Close()
				return errors.New("recipe move source must be a regular file")
			}
			data, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
			f.Close()
			if err != nil || len(data) > 4<<20 {
				sourceRoot.Close()
				return errors.New("recipe file unreadable or too large")
			}
			files[src] = data
		}
		sourceRoot.Close()
	}
	c := p.Config
	vars := strings.NewReplacer("{{dbName}}", c.DBName, "{{serverEndpoints}}", fmt.Sprintf("endpoint_add_tcp \"0.0.0.0:%d\"\nendpoint_add_udp \"0.0.0.0:%d\"", c.CFXPort, c.CFXPort), "{{maxClients}}", "48", "{{addPrincipalsMaster}}", "", "{{svLicense}}", "", "{{dbConnectionString}}", "")
	moved := map[string]bool{}
	for i, t := range r.Tasks {
		progress(Progress{Text: fmt.Sprintf("RECIPE %d / %s", i+1, t.Action)})
		switch t.Action {
		case "move_path":
			src, _ := recipePath(t.Src)
			dest, _ := recipePath(t.Dest)
			data, ok := files[src]
			if !ok {
				return fmt.Errorf("recipe source %s unavailable", src)
			}
			if _, exists := files[dest]; exists {
				return fmt.Errorf("recipe would overwrite %s", dest)
			}
			files[dest] = append([]byte{}, data...)
			delete(files, src)
			moved[dest] = true
		case "replace_string":
			names, _ := recipeFiles(t)
			for _, name := range names {
				data, ok := files[name]
				if !ok {
					return fmt.Errorf("recipe file %s unavailable", name)
				}
				s := string(data)
				if t.Mode == "all_vars" {
					s = vars.Replace(s)
				} else {
					s = strings.ReplaceAll(s, t.Search, vars.Replace(t.Replace))
				}
				files[name] = []byte(s)
			}
		case "query_database":
			names, _ := recipeFiles(t)
			for _, name := range names {
				if !safeDatabaseBootstrap(string(files[name]), c.DBName) {
					return errors.New("recipe SQL is not a create/select-only bootstrap for the new database; arbitrary SQL is unsupported")
				}
			}
		}
	}
	if !moved["server.cfg"] {
		return errors.New("recipe must provide server.cfg")
	}
	for name := range moved {
		s := string(files[name])
		if strings.Contains(s, "{{") {
			return fmt.Errorf("unresolved recipe variables in %s", name)
		}
		dir := "recipe-output/"
		if strings.HasSuffix(name, ".cfg") {
			dir = "server-data/"
			s = regexp.MustCompile(`(?mi)^\s*(sv_licenseKey\b|set\s+mysql_connection_string\b)[^\r\n]*`).ReplaceAllString(s, "# Credentials supplied privately by the Kora launcher")
			if name == "server.cfg" {
				s = regexp.MustCompile(`(?mi)^\s*sv_enforceGameBuild\s+[^\r\n]+`).ReplaceAllString(s, "sv_enforceGameBuild "+c.GameBuild)
			}
		}
		if err := writeNew(root, dir+name, []byte(s), 0600); err != nil {
			return err
		}
	}
	return writeNew(root, "recipe-lock.json", jsonBytes(r.Sources), 0600)
}
func safeDatabaseBootstrap(sql, name string) bool {
	sql = regexp.MustCompile(`(?m)--[^\r\n]*`).ReplaceAllString(sql, "")
	pattern := `(?is)^\s*CREATE\s+DATABASE\s+IF\s+NOT\s+EXISTS\s+` + "`" + regexp.QuoteMeta(name) + "`" + `\s+DEFAULT\s+CHARACTER\s+SET\s+utf8mb4\s+COLLATE\s+utf8mb4_unicode_ci\s*;\s*USE\s+` + "`" + regexp.QuoteMeta(name) + "`" + `\s*;\s*$`
	return regexp.MustCompile(pattern).MatchString(sql)
}
