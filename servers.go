package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Server struct {
	Path, Status string
	DockerState  string
	Managed      bool
}
type Checkout struct {
	Resource
	Current, State string
}
type UpdatePlan struct {
	Server    Server
	Resources []Checkout
}

func regularFile(path string) bool {
	i, err := os.Lstat(path)
	return err == nil && i.Mode().IsRegular()
}

// Search only known roots and their direct children, never an entire disk or database tree.
func discoverServers(ctx context.Context, c Config) []Server {
	return discoverServersWith(ctx, c, runProbe)
}

func discoverServersWith(ctx context.Context, c Config, run commandRunner) []Server {
	seen := map[string]bool{}
	var found []Server
	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		abs, err = filepath.EvalSymlinks(abs)
		if err != nil || seen[abs] {
			return
		}
		seen[abs] = true
		if !regularFile(filepath.Join(abs, "compose.yaml")) && !regularFile(filepath.Join(abs, "native.json")) {
			return
		}
		managed := regularFile(filepath.Join(abs, "start.py")) && regularFile(filepath.Join(abs, "server-data", "server.cfg"))
		legacy := regularFile(filepath.Join(abs, "resources.manifest"))
		if !managed && !legacy {
			return
		}
		status := "Existing development setup"
		if managed {
			status = "Complete"
			if !regularFile(filepath.Join(abs, "install.complete")) {
				status = "Incomplete — inspection only"
			}
		}
		found = append(found, Server{Path: abs, Status: status, Managed: managed, DockerState: "Docker unavailable"})
	}
	for _, path := range registeredServers() {
		add(path)
	}
	if c.Server != "" {
		add(c.Server)
	}
	add(c.Output)
	entries, _ := os.ReadDir(c.Output)
	for _, entry := range entries {
		if entry.IsDir() {
			add(filepath.Join(c.Output, entry.Name()))
		}
	}
	cwd, _ := os.Getwd()
	for i := 0; i < 3; i++ {
		add(cwd)
		cwd = filepath.Dir(cwd)
	}
	// Compose knows the working directories of running/stopped projects, including custom roots.
	if out, err := run(ctx, "docker", "compose", "ls", "--all", "--format", "json"); err == nil {
		for i := range found {
			found[i].DockerState = "Not started"
		}
		var projects []struct{ ConfigFiles, Status string }
		if json.Unmarshal([]byte(out), &projects) == nil {
			for _, p := range projects {
				for _, f := range strings.Split(p.ConfigFiles, ",") {
					dir := filepath.Dir(strings.TrimSpace(f))
					add(dir)
					resolved, _ := filepath.EvalSymlinks(dir)
					for i := range found {
						if found[i].Path == resolved {
							found[i].DockerState = p.Status
						}
					}
				}
			}
		}
	}
	for i := range found {
		if regularFile(filepath.Join(found[i].Path, "native.json")) {
			found[i].DockerState = nativeStatus(found[i].Path)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := backgroundCommand(exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...))
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func resourceDirectory(server Server) string {
	root := filepath.Join(server.Path, "resources")
	if _, err := os.Stat(root); os.IsNotExist(err) && server.Managed {
		return filepath.Join(server.Path, "server-data", "resources")
	}
	return root
}

func inspectServer(ctx context.Context, server Server, progress func(Progress)) ([]Checkout, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("install Git to inspect and update resources: https://git-scm.com/downloads")
	}
	root := resourceDirectory(server)
	if _, err := os.Stat(root); err != nil {
		return nil, errors.New("no resources folder found; this installation may be incomplete")
	}
	var list []Checkout
	var visit func(string, int) error
	visit = func(dir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, gitErr := os.Lstat(filepath.Join(path, ".git"))
			if gitErr != nil && !regularFile(filepath.Join(path, "fxmanifest.lua")) && !regularFile(filepath.Join(path, "__resource.lua")) && depth < 2 {
				if err := visit(path, depth+1); err != nil {
					return err
				}
				continue
			}
			rel, _ := filepath.Rel(root, path)
			r := Checkout{Resource: Resource{Path: filepath.ToSlash(rel)}, State: "No Git history — archives cannot be updated safely"}
			progress(Progress{Text: "INSPECT " + r.Path})
			if gitErr == nil && info.IsDir() {
				status, err := gitOutput(ctx, path, "status", "--porcelain", "--untracked-files=all")
				r.Current, gitErr = gitOutput(ctx, path, "rev-parse", "HEAD")
				remote, remoteErr := gitOutput(ctx, path, "remote", "get-url", "origin")
				remote = strings.TrimPrefix(remote, "ssh://git@github.com/")
				if strings.HasPrefix(remote, "git@github.com:") {
					remote = strings.TrimPrefix(remote, "git@github.com:")
				}
				if !strings.Contains(remote, "://") {
					remote = "https://github.com/" + remote
				}
				_, valid := repoSlug(remote)
				switch {
				case err != nil || gitErr != nil || remoteErr != nil:
					r.State = "Cannot read checkout"
				case valid != nil:
					r.State = "Origin must be a credential-free GitHub repository"
				case status != "":
					r.State = "Local edits/untracked files — commit or move them before staging"
				default:
					r.State = "Clean"
					r.Repository = remote
				}
			} else if gitErr == nil {
				r.State = "Linked worktree or symlink — manage this checkout with Git directly"
			}
			list = append(list, r)
			if len(list) > 500 {
				return errors.New("more than 500 resources; narrow this server's resource tree")
			}
		}
		return ctx.Err()
	}
	if err := visit(root, 0); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("no resource checkouts found")
	}
	return list, nil
}

func prepareUpdate(ctx context.Context, server Server, tag string, catalogSource string, progress func(Progress)) (UpdatePlan, error) {
	p := UpdatePlan{Server: server}
	if server.Managed && !regularFile(filepath.Join(server.Path, "install.complete")) {
		return p, errors.New("incomplete installations cannot be updated; create a fresh installation")
	}
	list, err := inspectServer(ctx, server, progress)
	if err != nil {
		return p, err
	}
	s := newSources()
	pins := map[string]Resource{}
	if catalogSource == "" {
		var saved struct {
			Source string `json:"source"`
		}
		if data, err := os.ReadFile(filepath.Join(server.Path, "catalog-source.json")); err == nil && len(data) < 16384 && json.Unmarshal(data, &saved) == nil {
			catalogSource = saved.Source
		}
	}
	if catalogSource != "" {
		catalog, _, err := loadCatalog(ctx, s.Client, catalogSource, defaultCatalogURL, fallbackCatalogURL)
		if err != nil {
			return p, err
		}
		pinned, err := catalogResources(catalog, nil, tag, false)
		if err != nil {
			return p, err
		}
		for _, r := range pinned {
			pins[strings.ToLower(strings.TrimSuffix(r.Repository, ".git"))] = r
		}
	}
	for i, r := range list {
		if r.State != "Clean" {
			return p, fmt.Errorf("%s: %s. Existing files are preserved", r.Path, r.State)
		}
		progress(Progress{Text: "RESOLVE " + r.Path, Done: i, Total: len(list)})
		resolved, known := pins[strings.ToLower(strings.TrimSuffix(r.Repository, ".git"))]
		var err error
		if known {
			resolved.Path = r.Path
		} else {
			resolved, err = s.resolve(ctx, r.Resource, tag)
		}
		if err != nil {
			return p, fmt.Errorf("%s: %w", r.Path, err)
		}
		r.Resource = resolved
		p.Resources = append(p.Resources, r)
	}
	return p, nil
}

// Stage a separate resource tree. Never fetch, reset, stash, switch, or write into live repositories.
func stageUpdate(ctx context.Context, p UpdatePlan, progress func(Progress)) (result Result) {
	parent := filepath.Join(p.Server.Path, "resource-updates")
	if info, err := os.Lstat(parent); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return Result{Err: errors.New("resource-updates must be a real directory")}
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		return Result{Err: err}
	}
	root, err := os.MkdirTemp(parent, "update-")
	if err != nil {
		return Result{Err: err}
	}
	result.Folder = root
	fail := func(err error) Result { return Result{Folder: root, Err: err} }
	if err := secureDir(root); err != nil {
		return fail(err)
	}
	var resources []Resource
	for i, r := range p.Resources {
		if !relativePath(r.Path) {
			return fail(errors.New("unsafe resource path"))
		}
		source := filepath.Join(resourceDirectory(p.Server), filepath.FromSlash(r.Path))
		status, err := gitOutput(ctx, source, "status", "--porcelain", "--untracked-files=all")
		head, headErr := gitOutput(ctx, source, "rev-parse", "HEAD")
		if err != nil || headErr != nil || status != "" || head != r.Current {
			return fail(fmt.Errorf("%s changed since review; inspect it again", r.Path))
		}
		dest := filepath.Join(root, "resources", filepath.FromSlash(r.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return fail(err)
		}
		progress(Progress{Text: "CLONE SNAPSHOT " + r.Path, Done: i, Total: len(p.Resources)})
		// Independent object copies retain local commits without sharing objects with the live tree.
		if _, err = gitOutput(ctx, root, "clone", "--no-hardlinks", "--no-checkout", "--", source, dest); err != nil {
			return fail(fmt.Errorf("%s: snapshot clone failed", r.Path))
		}
		if _, err = gitOutput(ctx, dest, "remote", "set-url", "origin", r.Repository); err != nil {
			return fail(err)
		}
		progress(Progress{Text: "FETCH " + r.Path + " @ " + r.Ref, Done: i, Total: len(p.Resources)})
		if _, err = gitOutput(ctx, dest, "fetch", "--no-recurse-submodules", "origin", r.Commit); err != nil {
			return fail(fmt.Errorf("%s: fetch failed; check Git authentication. Partial snapshot retained", r.Path))
		}
		if _, err = gitOutput(ctx, dest, "merge-base", "--is-ancestor", r.Current, r.Commit); err != nil {
			return fail(fmt.Errorf("%s: target would omit local commits or move backwards; retained for manual review", r.Path))
		}
		if _, err = gitOutput(ctx, dest, "checkout", "--detach", r.Commit); err != nil {
			return fail(fmt.Errorf("%s: checkout failed", r.Path))
		}
		resources = append(resources, r.Resource)
	}
	if err = writeNew(root, "resource-lock.json", jsonBytes(resources), 0600); err != nil {
		return fail(err)
	}
	instructions := "Kora resource update staged for review.\n\nSource server: " + p.Server.Path + "\n\nThe live resource mounts and database have not been changed. Compare the old and new trees, carry over required ignored configuration/build outputs privately, and run each resource's builds. Review upstream migration requirements.\n\nTo adopt: stop the affected server, point its resource bind mounts at the new resources directory, then restart. For the original setup, preserve its individual group mount targets. Keep the previous mount paths for rollback. Do not run database migrations as part of this resource-only update.\n"
	if err = writeNew(root, "REVIEW.txt", []byte(instructions), 0600); err != nil {
		return fail(err)
	}
	if err = writeNew(root, "update.complete", []byte("Resource snapshot complete; not activated\n"), 0600); err != nil {
		return fail(err)
	}
	return result
}

func templateFor(list []Resource, ensure []string) Recipe {
	r := Recipe{Version: 1, Replace: true, Resources: append([]Resource{}, list...), Ensure: append([]string{}, ensure...)}
	for i := range r.Resources {
		r.Resources[i].Commit = ""
		r.Resources[i].SelectionNote = ""
	}
	return r
}

func exportTemplate(path string, list []Resource, ensure []string) error {
	if list == nil {
		var err error
		list, err = resources(Recipe{})
		if err != nil {
			return err
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Parent must already exist. Never overwrite a template or create unexpected parent paths.
	return writeNew(filepath.Dir(path), filepath.Base(path), jsonBytes(templateFor(list, ensure)), 0600)
}
