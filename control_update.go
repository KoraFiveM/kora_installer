package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Backups are new private directories. Never copy database data files while its
// service is running: the database is backed up using its logical dump client.
func copyBackupTree(ctx context.Context, source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("backup refuses symbolic link: %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.Mkdir(dest, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported backup file: %s", path)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, backupReader{ctx, in})
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

func backupParent(server, parent string) (string, error) {
	root, err := filepath.EvalSymlinks(server)
	if err != nil {
		return "", err
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	root, _ = filepath.Abs(root)
	parent, _ = filepath.Abs(parent)
	rel, err := filepath.Rel(strings.ToLower(root), strings.ToLower(parent))
	if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))) {
		return "", errors.New("choose a backup folder outside the server directory")
	}
	return parent, nil
}

func dumpServerDatabase(ctx context.Context, root, backup string) error {
	values, err := readEnvironment(root)
	if err != nil {
		return err
	}
	var client string
	for _, name := range []string{"mariadb-dump.exe", "mysqldump.exe", "mariadb-dump", "mysqldump"} {
		path := filepath.Join(root, "database", "bin", name)
		if regularFile(path) {
			client = path
			break
		}
		if path, e := exec.LookPath(name); e == nil {
			client = path
			break
		}
	}
	if client == "" {
		return errors.New("database backup requires mariadb-dump or mysqldump: install the database client tools and add their bin folder to PATH")
	}
	if strings.HasPrefix(values["DB_NAME"], "-") {
		return errors.New("invalid database name")
	}
	port, err := strconv.Atoi(values["DB_PORT"])
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid database port in environment")
	}
	for _, key := range []string{"DB_HOST", "DB_NAME", "DB_USER", "DB_PASSWORD"} {
		if values[key] == "" {
			return fmt.Errorf("missing %s for database backup", key)
		}
	}
	// The dedicated database service retains its administrator credentials locally.
	if meta, e := nativeMeta(root); e == nil && meta.DatabaseService != "" {
		var admin struct {
			User     string `json:"user"`
			Password string `json:"password"`
		}
		data, e := os.ReadFile(filepath.Join(root, "database-admin.json"))
		if e != nil || json.Unmarshal(data, &admin) != nil || admin.User == "" || admin.Password == "" {
			return errors.New("cannot read dedicated database backup credentials")
		}
		values["DB_USER"] = admin.User
		values["DB_PASSWORD"] = admin.Password
	}
	// Option-file quoting prevents passwords entering process command lines.
	quote := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`).Replace(s) + `"`
	}
	option := filepath.Join(backup, ".dump-client.cnf")
	data := "[client]\nprotocol=tcp\nhost=" + quote(values["DB_HOST"]) + "\nport=" + strconv.Itoa(port) + "\nuser=" + quote(values["DB_USER"]) + "\npassword=" + quote(values["DB_PASSWORD"]) + "\n"
	if err = os.WriteFile(option, []byte(data), 0600); err != nil {
		return err
	}
	defer os.Remove(option)
	output, err := os.OpenFile(filepath.Join(backup, "database.sql"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, client, "--defaults-file="+option, "--single-transaction", "--quick", "--hex-blob", "--default-character-set=utf8mb4", "--skip-lock-tables", "--no-tablespaces", "--routines", "--events", "--triggers", "--databases", values["DB_NAME"])
	backgroundCommand(cmd)
	cmd.Stdout = output
	err = cmd.Run()
	syncErr := output.Sync()
	closeErr := output.Close()
	if err != nil {
		return errors.New("database dump failed; no resources updated. Check database client compatibility and account permissions")
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if info, e := os.Stat(filepath.Join(backup, "database.sql")); e != nil || info.Size() == 0 {
		return errors.New("database dump was empty; update cancelled")
	}
	return nil
}

func backupServer(ctx context.Context, s Server, parent string, dump func(context.Context, string, string) error) (string, error) {
	parent, err := backupParent(s.Path, parent)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(parent, "kora-backup-")
	if err != nil {
		return "", err
	}
	if err = secureDir(dir); err != nil {
		return dir, err
	}
	// Root files contain environment, metadata and configuration. server-data
	// includes resources (and their .git histories), configs and txAdmin state.
	entries, err := os.ReadDir(s.Path)
	if err != nil {
		return dir, err
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(strings.ToLower(entry.Name()), ".exe") || entry.Name() == "artifact.download" {
			continue
		}
		src := filepath.Join(s.Path, entry.Name())
		if !regularFile(src) {
			return dir, fmt.Errorf("unsafe configuration file: %s", entry.Name())
		}
		if err = copyNewFile(src, filepath.Join(dir, entry.Name())); err != nil {
			return dir, err
		}
	}
	for _, name := range []string{"server-data", "resources", "runtime"} {
		source := filepath.Join(s.Path, name)
		if _, err = os.Lstat(source); os.IsNotExist(err) {
			continue
		}
		if err = copyBackupTree(ctx, source, filepath.Join(dir, name)); err != nil {
			return dir, err
		}
	}
	if err = dump(ctx, s.Path, dir); err != nil {
		return dir, err
	}
	err = writeNew(dir, "backup.complete", []byte("Resources, configuration and logical database dump complete. Keep private: contains credentials.\n"), 0600)
	return dir, err
}

func checkUpdateClean(ctx context.Context, p UpdatePlan) error {
	for _, r := range p.Resources {
		if !relativePath(r.Path) {
			return errors.New("unsafe resource path")
		}
		path := filepath.Join(resourceDirectory(p.Server), filepath.FromSlash(r.Path))
		if err := verifyUpdateWorktree(ctx, path); err != nil {
			return err
		}
		status, err := gitOutput(ctx, path, "status", "--porcelain", "--untracked-files=all")
		head, e := gitOutput(ctx, path, "rev-parse", "HEAD")
		if err != nil || e != nil || status != "" || head != r.Current {
			return fmt.Errorf("%s has local changes or changed since review. Update cancelled", r.Path)
		}
	}
	return nil
}

func applyControlUpdate(ctx context.Context, p UpdatePlan, parent string, emit func(Progress), stop func(context.Context, string) error, dump func(context.Context, string, string) error) (string, error) {
	if len(p.Resources) == 0 {
		return "", errors.New("no Kora Git resources found")
	}
	if _, err := backupParent(p.Server.Path, parent); err != nil {
		return "", err
	}
	if err := checkUpdateClean(ctx, p); err != nil {
		return "", err
	}
	emit(Progress{Text: "Stopping FiveM before backup"})
	if err := stop(ctx, p.Server.Path); err != nil {
		return "", err
	}
	emit(Progress{Text: "Backing up resources, configuration and database"})
	backup, err := backupServer(ctx, p.Server, parent, dump)
	if err != nil {
		return backup, err
	}
	// Work on full copies, including ignored configuration/build outputs. Git
	// checkout refuses collisions; no reset/clean/stash is used anywhere.
	stage, err := os.MkdirTemp(p.Server.Path, "control-update-")
	if err != nil {
		return backup, err
	}
	if err = secureDir(stage); err != nil {
		return backup, err
	}
	for i, r := range p.Resources {
		dest := filepath.Join(stage, strconv.Itoa(i))
		source := filepath.Join(resourceDirectory(p.Server), filepath.FromSlash(r.Path))
		emit(Progress{Text: "Updating " + r.Path, Done: i, Total: len(p.Resources)})
		if err = copyBackupTree(ctx, source, dest); err != nil {
			return backup, err
		}
		if err = verifyUpdateWorktree(ctx, dest); err != nil {
			return backup, err
		}
		if _, err = gitOutput(ctx, dest, "fetch", "--no-recurse-submodules", "origin", r.Commit); err != nil {
			return backup, fmt.Errorf("%s: fetch failed; live resources unchanged", r.Path)
		}
		if _, err = gitOutput(ctx, dest, "merge-base", "--is-ancestor", r.Current, r.Commit); err != nil {
			return backup, fmt.Errorf("%s: update would omit local commits", r.Path)
		}
		if _, err = gitOutput(ctx, dest, "checkout", "--no-overwrite-ignore", "--detach", r.Commit); err != nil {
			return backup, fmt.Errorf("%s: checkout failed; live resources unchanged", r.Path)
		}
	}
	if err = checkUpdateClean(ctx, p); err != nil {
		return backup, err
	}
	if err = ctx.Err(); err != nil {
		return backup, err
	}
	// Commit the replacements without cancellation between renames. Retain old
	// directories for rollback, including when a later rename fails.
	swapped := 0
	rollback := func(cause error) error {
		for j := swapped - 1; j >= 0; j-- {
			live := filepath.Join(resourceDirectory(p.Server), filepath.FromSlash(p.Resources[j].Path))
			if e := os.Rename(live, filepath.Join(stage, fmt.Sprintf("failed-%d", j))); e != nil {
				return fmt.Errorf("%w; rollback failed: %v; retained files: %s", cause, e, stage)
			}
			if e := os.Rename(filepath.Join(stage, fmt.Sprintf("previous-%d", j)), live); e != nil {
				return fmt.Errorf("%w; rollback failed: %v; retained files: %s", cause, e, stage)
			}
		}
		return cause
	}
	for i, r := range p.Resources {
		live := filepath.Join(resourceDirectory(p.Server), filepath.FromSlash(r.Path))
		old := filepath.Join(stage, fmt.Sprintf("previous-%d", i))
		if err = os.Rename(live, old); err != nil {
			return backup, rollback(err)
		}
		if err = os.Rename(filepath.Join(stage, strconv.Itoa(i)), live); err != nil {
			restore := os.Rename(old, live)
			return backup, rollback(errors.Join(err, restore))
		}
		swapped++
	}
	return backup, writeNew(stage, "update.complete", jsonBytes(p.Resources), 0600)
}

// A copied repository must not redirect Git writes back into the live checkout.
func verifyUpdateWorktree(ctx context.Context, path string) error {
	info, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("update requires an independent Git directory")
	}
	top, err := gitOutput(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return errors.New("cannot verify resource working directory")
	}
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	top, _ = filepath.Abs(top)
	expected, _ = filepath.Abs(expected)
	if !strings.EqualFold(top, expected) {
		return errors.New("resource Git configuration redirects its working directory; update cancelled")
	}
	return nil
}

type backupReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r backupReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
