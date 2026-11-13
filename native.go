package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type nativeInstallation struct {
	Version                  int
	Service, DatabaseService string
}

func nativeMeta(root string) (nativeInstallation, error) {
	var n nativeInstallation
	data, e := os.ReadFile(filepath.Join(root, "native.json"))
	if e == nil {
		e = json.Unmarshal(data, &n)
	}
	if e == nil && n.Version != 1 {
		e = errors.New("unsupported native installation")
	}
	return n, e
}
func nativeResources(root string, c Config) string {
	if c.Docker {
		return filepath.Join(root, "resources")
	}
	return filepath.Join(root, "server-data", "resources")
}
func copyNewFile(source, dest string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}
func installNativeFiles(root string, c Config) (nativeInstallation, error) {
	id, e := randomID(8)
	if e != nil {
		return nativeInstallation{}, e
	}
	n := nativeInstallation{Version: 1}
	if c.WindowsService {
		n.Service = "Kora-" + id
	}
	if c.DBSetup == "new-service" {
		n.DatabaseService = "KoraDB-" + id
	}
	executable, e := os.Executable()
	if e != nil {
		return n, e
	}
	if e = copyNewFile(executable, filepath.Join(root, "kora.exe")); e != nil {
		return n, e
	}
	if e = writeNew(root, "native.json", jsonBytes(n), 0600); e != nil {
		return n, e
	}
	mode := "--native-run"
	if c.WindowsService {
		mode = "--native-start"
	}
	e = writeNew(root, "start.cmd", []byte("@echo off\r\n\"%~dp0kora.exe\" "+mode+" \"%~dp0.\"\r\npause\r\n"), 0600)
	return n, e
}

func verifyChecksum(file string, sums []byte, name string) error {
	expected := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./") == name {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if len(expected) != 64 {
		return errors.New("official archive checksum missing")
	}
	if _, e := hex.DecodeString(expected); e != nil {
		return errors.New("invalid archive checksum")
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("archive checksum mismatch")
	}
	return nil
}
func installMariaDB(ctx context.Context, root string, c *Config, n nativeInstallation, emit func(Progress)) error {
	name := "mariadb-" + c.MariaDBVersion + "-winx64.zip"
	base := "https://archive.mariadb.org/mariadb-" + c.MariaDBVersion + "/winx64-packages/"
	emit(Progress{Text: "Downloading MariaDB " + c.MariaDBVersion + " and official SHA-256 manifest"})
	archive := filepath.Join(root, "mariadb.download.zip")
	if e := download(ctx, base+name, archive); e != nil {
		return e
	}
	sums, e := newSources().get(ctx, base+"sha256sums.txt", 1<<20)
	if e != nil {
		return e
	}
	if e = verifyChecksum(archive, sums, name); e != nil {
		return e
	}
	dest := filepath.Join(root, "database")
	if e = os.Mkdir(dest, 0700); e != nil {
		return e
	}
	if e = extractZipProgress(archive, dest, true, func(name string) { emit(Progress{Text: "MARIADB EXTRACT " + name}) }); e != nil {
		return e
	}
	password, e := randomID(24)
	if e != nil {
		return e
	}
	c.DBHost = "127.0.0.1"
	c.DBAdmin = "root"
	c.DBAdminPassword = password
	if e = writeNew(root, "database-admin.json", jsonBytes(map[string]string{"user": "root", "password": password}), 0600); e != nil {
		return e
	}
	emit(Progress{Text: "Creating isolated MariaDB service " + n.DatabaseService})
	return createMariaService(ctx, root, *c, n.DatabaseService)
}

func nativeSession(root string) (*exec.Cmd, error) {
	if !regularFile(filepath.Join(root, "install.complete")) {
		return nil, errors.New("installation is incomplete")
	}
	values, e := readEnvironment(root)
	if e != nil {
		return nil, e
	}
	runtimeDir := filepath.Join(root, "runtime")
	if e = os.MkdirAll(runtimeDir, 0700); e != nil {
		return nil, e
	}
	session, e := os.MkdirTemp(runtimeDir, "session-")
	if e != nil {
		return nil, e
	}
	if e = secureDir(session); e != nil {
		return nil, e
	}
	for _, key := range []string{"CFX_LICENSE_KEY", "DB_NAME"} {
		if !safeText(values[key]) {
			return nil, fmt.Errorf("invalid %s", key)
		}
	}
	connection := (&url.URL{Scheme: "mysql", User: url.UserPassword(values["DB_USER"], values["DB_PASSWORD"]), Host: net.JoinHostPort(values["DB_HOST"], values["DB_PORT"]), Path: "/" + values["DB_NAME"], RawQuery: "charset=utf8mb4"}).String()
	if e = writeNew(session, "credentials.cfg", []byte(fmt.Sprintf("sv_licenseKey %q\nset mysql_connection_string %q\n", values["CFX_LICENSE_KEY"], connection)), 0600); e != nil {
		return nil, e
	}
	rel, _ := filepath.Rel(filepath.Join(root, "server-data"), session)
	rel = filepath.ToSlash(rel)
	if e = writeNew(session, "server.cfg", []byte(fmt.Sprintf("exec %q\nexec server.cfg\n", rel+"/credentials.cfg")), 0600); e != nil {
		return nil, e
	}
	args := []string{"+set", "onesync", "on"}
	cmd := exec.Command(filepath.Join(root, "artifacts", "FXServer.exe"))
	cmd.Dir = filepath.Join(root, "server-data")
	cmd.Env = os.Environ()
	if values["TXADMIN_ENABLED"] == "true" {
		// Persistent txAdmin state is separate from immutable credential sessions.
		tx := filepath.Join(session, "txData")
		entries, _ := os.ReadDir(runtimeDir)
		var previous []string
		for _, entry := range entries {
			candidate := filepath.Join(runtimeDir, entry.Name())
			if candidate != session && regularFile(filepath.Join(candidate, "ready")) && regularFile(filepath.Join(candidate, "txData", "default", "config.json")) {
				previous = append(previous, candidate)
			}
		}
		sort.Slice(previous, func(i, j int) bool {
			a, _ := os.Stat(previous[i])
			b, _ := os.Stat(previous[j])
			return a.ModTime().Before(b.ModTime())
		})
		oldConfig := ""
		if len(previous) > 0 {
			oldTx := filepath.Join(previous[len(previous)-1], "txData")
			if _, err := os.Stat(oldTx); err == nil {
				if err = copyNativeState(oldTx, tx); err != nil {
					return nil, err
				}
				oldConfig = filepath.Join(oldTx, "default", "config.json")
				if !regularFile(oldConfig) {
					return nil, errors.New("previous txAdmin config is not a regular file")
				}
			}
		}
		profile := filepath.Join(tx, "default")
		if e = os.MkdirAll(profile, 0700); e != nil {
			return nil, e
		}
		cfg := map[string]any{}
		if data, err := os.ReadFile(oldConfig); err == nil {
			if err = json.Unmarshal(data, &cfg); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		cfg["version"] = 2
		server, _ := cfg["server"].(map[string]any)
		if server == nil {
			server = map[string]any{}
		}
		server["dataPath"] = cmd.Dir
		server["cfgPath"] = rel + "/server.cfg"
		server["onesync"] = "on"
		server["autoStart"] = true
		cfg["server"] = server
		if e = writeNew(profile, "config.json", jsonBytes(cfg), 0600); e != nil {
			return nil, e
		}
		cmd.Env = append(cmd.Env, "TXHOST_DATA_PATH="+tx, "TXHOST_TXA_PORT="+values["TXADMIN_PORT"], "TXHOST_GAME_NAME=fivem")
	} else {
		args = append(args, "+exec", rel+"/server.cfg")
	}
	if e = writeNew(session, "ready", []byte("prepared\n"), 0600); e != nil {
		return nil, e
	}
	cmd.Args = append(cmd.Args, args...)
	return cmd, nil
}
func nativeLogs(root string) string {
	f, e := os.Open(filepath.Join(root, "runtime", "server.log"))
	if e != nil {
		return ""
	}
	defer f.Close()
	if st, e := f.Stat(); e == nil && st.Size() > 16384 {
		f.Seek(-16384, io.SeekEnd)
	}
	b, _ := io.ReadAll(io.LimitReader(f, 16384))
	text := ansi.Strip(string(b))
	values, _ := readEnvironment(root)
	for key, value := range values {
		if value != "" && (strings.Contains(key, "PASSWORD") || strings.Contains(key, "LICENSE_KEY")) {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	return text
}
func startNative(ctx context.Context, root string, emit func(Progress)) error {
	c, e := serverConfig(root)
	if e != nil {
		return e
	}
	emit(Progress{Done: 0, Total: 3, Text: "Starting native FXServer"})
	if e = nativeAction(ctx, root, "start"); e != nil {
		return e
	}
	emit(Progress{Done: 2, Total: 3, Text: "Waiting for native FXServer /info.json; txAdmin may require initial setup"})
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	last := ""
	started := time.Now()
	for {
		logs := nativeLogs(root)
		if logs != last {
			last = logs
			lines := strings.Split(strings.TrimSpace(logs), "\n")
			for _, line := range lines[max(0, len(lines)-12):] {
				emit(Progress{Done: 2, Total: 3, Text: line})
			}
		}
		if time.Since(started) > 3*time.Second && strings.Contains(nativeStatus(root), "stopped") {
			return errors.New("native FXServer stopped before readiness; inspect runtime/server.log")
		}
		if fiveMReady(ctx, client, fmt.Sprintf("http://127.0.0.1:%d/info.json", c.CFXPort)) {
			emit(Progress{Done: 3, Total: 3, Text: "Native FXServer is responding"})
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("FXServer readiness timed out; inspect runtime/server.log and txAdmin")
		case <-time.After(2 * time.Second):
		}
	}
}

func copyNativeState(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == filepath.Join("default", "config.json") {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("txAdmin state contains a symlink")
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		return copyNewFile(path, target)
	})
}
