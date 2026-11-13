package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledTXRecipe(t *testing.T) {
	r, list, err := parseTXRecipe(bundledTXRecipe)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 40 || len(r.Sources) != 1 {
		t.Fatal("missing recipe sources")
	}
	legacy := []byte("$engine: 3\ntasks:\n- action: download_github\n  src: https://github.com/KoraFiveM/PolyZone\n  ref: main\n  dest: ./resources/[standalone]/PolyZone\n")
	_, oldList, err := parseTXRecipe(legacy)
	if err != nil || oldList[0].Repository != "https://github.com/mkafrin/PolyZone.git" || oldList[0].Ref != "master" {
		t.Fatal("legacy PolyZone must use public upstream")
	}
	validated, err := resources(Recipe{Resources: list, Replace: true})
	if err != nil || len(validated) != len(list) {
		t.Fatalf("recipe must replace default manifest: %v", err)
	}
	for _, bad := range []string{strings.Replace(string(bundledTXRecipe), "action: waste_time", "action: execute", 1), strings.Replace(string(bundledTXRecipe), "./tmp/kora-recipe", "../escape", 1)} {
		if _, _, err := parseTXRecipe([]byte(bad)); err == nil {
			t.Fatal("accepted unsafe action/path")
		}
	}
}
func TestTXBootstrapSQL(t *testing.T) {
	sql := "CREATE DATABASE IF NOT EXISTS `kora_test` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; USE `kora_test`;"
	if !safeDatabaseBootstrap(sql, "kora_test") {
		t.Fatal("rejected safe bootstrap")
	}
	for _, s := range []string{sql + " DROP DATABASE old;", strings.ReplaceAll(sql, "kora_test", "existing"), "", "SELECT 1;"} {
		if safeDatabaseBootstrap(s, "kora_test") {
			t.Fatal("accepted unsupported SQL")
		}
	}
}
func TestTXRecipeKeepsSourceGitAndPrivateCredentials(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git required")
	}
	source := t.TempDir()
	gitTest(t, source, "init")
	gitTest(t, source, "config", "user.email", "fixture@example.invalid")
	gitTest(t, source, "config", "user.name", "Fixture")
	cfg := "{{serverEndpoints}}\nsv_licenseKey \"{{svLicense}}\"\nset mysql_connection_string \"{{dbConnectionString}}\"\nsv_enforceGameBuild 1\nensure mapmanager\n"
	os.WriteFile(filepath.Join(source, "server.cfg"), []byte(cfg), 0600)
	gitTest(t, source, "add", ".")
	gitTest(t, source, "commit", "-m", "fixture")
	recipe := []byte("$engine: 3\ntasks:\n- action: download_github\n  src: https://github.com/example/config\n  dest: ./tmp/config\n- action: move_path\n  src: ./tmp/config/server.cfg\n  dest: ./server.cfg\n- action: replace_string\n  mode: all_vars\n  file: ./server.cfg\n- action: download_github\n  src: https://github.com/example/resource\n  dest: ./resources/[custom]/resource\n")
	r, _, err := parseTXRecipe(recipe)
	if err != nil {
		t.Fatal(err)
	}
	r.Sources[0].Repository = source
	r.Sources[0].Commit = gitTest(t, source, "rev-parse", "HEAD")
	c := validConfig(t)
	c.GameBuild = "3751"
	root := t.TempDir()
	p := Plan{Config: c, TXRecipe: r}
	if err := generate(root, p); err != nil {
		t.Fatal(err)
	}
	if err := applyTXRecipe(context.Background(), root, p, func(Progress) {}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "server-data", "server.cfg"))
	text := string(data)
	if strings.Contains(text, c.License) || strings.Contains(text, c.DBPassword) || strings.Contains(text, "{{") || !strings.Contains(text, "sv_enforceGameBuild 3751") {
		t.Fatal("invalid rendered configuration")
	}
	clone := filepath.Join(root, "recipe-sources", "source-000")
	if got := gitTest(t, clone, "status", "--porcelain"); got != "" {
		t.Fatal("modified source checkout")
	}
	if err := applyTXRecipe(context.Background(), root, p, func(Progress) {}); err == nil {
		t.Fatal("overwrote existing recipe output")
	}
}
