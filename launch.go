package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func readEnvironment(root string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(root, ".env"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		var value string
		if !ok || json.Unmarshal([]byte(raw), &value) != nil {
			return nil, errors.New("invalid installer .env; values must be JSON strings")
		}
		values[key] = value
	}
	return values, s.Err()
}

func dockerCommand(ctx context.Context, root string, args ...string) (*exec.Cmd, error) {
	if !regularFile(filepath.Join(root, "install.complete")) {
		return nil, errors.New("installation is incomplete; create a new installation and retain these files for inspection")
	}
	values, err := readEnvironment(root)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "--project-directory", root, "--env-file", filepath.Join(root, ".compose-env"), "-f", filepath.Join(root, "compose.yaml")}, args...)...)
	cmd.Dir = root
	// Do not let ambient Compose overrides point a new installation at another project.
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if _, exists := values[key]; !exists && !strings.HasPrefix(strings.ToUpper(key), "COMPOSE_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	for key, value := range values {
		if strings.HasPrefix(key, "DB_") || strings.HasPrefix(key, "CFX_") || strings.HasPrefix(key, "TXADMIN_") || strings.HasPrefix(key, "ADMINER_") || key == "DOCKER" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	return cmd, nil
}

func checkPorts(c Config) error {
	ports := []int{c.CFXPort}
	if !c.Docker && c.DBSetup == "new-service" {
		ports = append(ports, c.DBPort)
	}
	if c.TXAdmin {
		ports = append(ports, c.TXPort)
	}
	if c.DBExposePort {
		ports = append(ports, c.DBHostPort)
	}
	if c.Adminer {
		ports = append(ports, c.AdminerPort)
	}
	for _, port := range ports {
		listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", port))
		if err != nil {
			return fmt.Errorf("TCP port %d is in use or unavailable. Choose a free port before installing or stop the conflicting server yourself", port)
		}
		listener.Close()
	}
	packet, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", c.CFXPort))
	if err != nil {
		return fmt.Errorf("UDP port %d is in use or unavailable. Choose a free FiveM port", c.CFXPort)
	}
	packet.Close()
	return nil
}

func serverConfig(root string) (Config, error) {
	values, err := readEnvironment(root)
	if err != nil {
		return Config{}, err
	}
	c := Config{Docker: values["DOCKER"] != "false", Adminer: values["ADMINER_ENABLED"] == "true", DBName: values["DB_NAME"], DBUser: values["DB_USER"], TXAdmin: values["TXADMIN_ENABLED"] == "true", DBExposePort: values["DB_EXPOSE_PORT"] == "true"}
	for key, dest := range map[string]*int{"CFX_PORT": &c.CFXPort, "TXADMIN_PORT": &c.TXPort, "DB_HOST_PORT": &c.DBHostPort, "ADMINER_PORT": &c.AdminerPort} {
		if (key == "DB_HOST_PORT" && !c.DBExposePort) || (key == "ADMINER_PORT" && !c.Adminer) {
			continue
		}
		n, e := strconv.Atoi(values[key])
		if e != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("invalid %s in server configuration", key)
		}
		*dest = n
	}
	return c, nil
}

func adminerURL(c Config) string {
	query := url.Values{"server": {"db"}, "username": {c.DBUser}, "db": {c.DBName}}
	return fmt.Sprintf("http://127.0.0.1:%d/?%s", c.AdminerPort, query.Encode())
}

// Check only the edited field so an unrelated busy default can be corrected later.
func checkEnteredPort(c Config, key string, port int) error {
	if key == "tx-port" && port == 30120 {
		return errors.New("txAdmin does not permit port 30120; choose another port")
	}
	services := []struct {
		key, name string
		port      int
		enabled   bool
	}{
		{"cfx-port", "FiveM", c.CFXPort, true},
		{"db-port", "new MariaDB service", c.DBPort, !c.Docker && c.DBSetup == "new-service"},
		{"tx-port", "txAdmin", c.TXPort, c.TXAdmin},
		{"db-host-port", "database", c.DBHostPort, c.DBExposePort},
		{"adminer-port", "Adminer", c.AdminerPort, c.Adminer},
	}
	for _, service := range services {
		if service.key != key && service.enabled && service.port == port {
			return fmt.Errorf("Port %d is already selected for %s; choose a different port", port, service.name)
		}
	}
	address := fmt.Sprintf(":%d", port)
	if key == "adminer-port" {
		address = fmt.Sprintf("127.0.0.1:%d", port)
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return fmt.Errorf("TCP port %d is in use or unavailable; choose another port", port)
	}
	defer listener.Close()
	if key == "cfx-port" {
		packet, err := net.ListenPacket("udp4", address)
		if err != nil {
			return fmt.Errorf("UDP port %d is in use or unavailable; choose another FiveM port", port)
		}
		packet.Close()
	}
	return nil
}
