package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Validate each page before navigation, without writing files or provisioning services.
func guiValidate(c Config, page int) error {
	if page >= 1 {
		if strings.TrimSpace(c.DirectoryName) == "" {
			return fmt.Errorf("Give this server a folder name, for example my-server")
		}
		if !filepath.IsAbs(c.Output) {
			return fmt.Errorf("Choose a full parent folder path with Browse")
		}
		if strings.TrimSpace(c.Hostname) == "" {
			return fmt.Errorf("Enter the name players should see")
		}
		if strings.TrimSpace(c.CFXUser) == "" {
			return fmt.Errorf("Enter your Cfx.re username, not your license key")
		}
		if strings.HasPrefix(c.CFXUser, "cfxk_") {
			return fmt.Errorf("That looks like a license key. Paste it in Server license key instead")
		}
		if strings.TrimSpace(c.License) == "" {
			return fmt.Errorf("Create a server license at portal.cfx.re, then paste the key here")
		}
		probe := c
		// Later pages may still contain a port conflict or incomplete DB settings.
		if page == 1 {
			probe.DBSetup = "existing"
			probe.DBHost = "127.0.0.1"
			probe.DBAdmin = "root"
		}
		if err := probe.Validate(); err != nil {
			return err
		}
		folder := filepath.Join(c.Output, c.DirectoryName)
		if _, err := os.Lstat(folder); err == nil {
			return fmt.Errorf("%s already exists. Choose a new folder name; your existing files will be kept", folder)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("Cannot inspect the destination: %w", err)
		}
		if err := checkEnteredPort(probe, "cfx-port", c.CFXPort); err != nil {
			return err
		}
		if c.TXAdmin {
			if err := checkEnteredPort(probe, "tx-port", c.TXPort); err != nil {
				return err
			}
		}
	}
	if page >= 2 {
		if c.DBSetup == "existing" && (strings.TrimSpace(c.DBHost) == "" || strings.TrimSpace(c.DBAdmin) == "") {
			return fmt.Errorf("Enter the existing database address and administrator username")
		}
		if err := c.Validate(); err != nil {
			return err
		}
		if err := checkPorts(c); err != nil {
			return err
		}
	}
	return nil
}

func guiRedact(text string, c Config) string {
	for _, secret := range []string{c.License, c.DBPassword, c.DBAdminPassword} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[hidden]")
		}
	}
	// Remove terminal escape sequences and control characters from native labels/logs.
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func guiReview(p Plan) string {
	c := p.Config
	database := "New private MariaDB service (existing databases are preserved)"
	if c.DBSetup == "existing" {
		database = fmt.Sprintf("Existing %s at %s:%d; create a NEW database and account", c.DBType, c.DBHost, c.DBPort)
	}
	service := "Start manually from Kora"
	if c.WindowsService {
		service = "Start automatically with Windows"
	}
	text := fmt.Sprintf("INSTALLATION PLAN\r\n\r\nFolder: %s\r\nServer: %s\r\nFiveM: %d TCP + UDP\r\nDatabase: %s\r\nDatabase port: %d\r\nStartup: %s\r\nGame build: %s\r\nArtifact: %s\r\n\r\n%d Git resource clones:\r\n", filepath.Join(c.Output, c.DirectoryName), c.Hostname, c.CFXPort, database, c.DBPort, service, c.GameBuild, p.ArtifactURL, len(p.Resources))
	if c.TXAdmin {
		text += fmt.Sprintf("txAdmin: http://127.0.0.1:%d\r\n", c.TXPort)
	}
	for _, r := range p.Resources {
		text += fmt.Sprintf("  %s @ %s %s\r\n", r.Path, r.Ref, r.SelectionNote)
	}
	return guiRedact(text, c)
}
