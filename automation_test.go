package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAutomationFlags(t *testing.T) {
	t.Setenv("CFX_LICENSE_KEY", "private-key")
	c, _, err := flags([]string{"--yes", "--start", "--name=my-server", "--cfx-user=user"}, io.Discard)
	if err != nil || !c.Yes || !c.Start || c.License != "private-key" || c.RecipeInput != "bundled" || c.KoraTag != "latest" {
		t.Fatalf("automation defaults: %+v %v", c, err)
	}
	for _, name := range []string{"../escape", "a/b", "CON", "hello world"} {
		c.DirectoryName = name
		if c.Validate() == nil {
			t.Fatalf("accepted name %s", name)
		}
	}
	c, _, err = flags([]string{"--template=./recipe.yaml"}, io.Discard)
	if err != nil || c.RecipeInput != "./recipe.yaml" || c.Template != "" {
		t.Fatal("YAML template not selected")
	}
}
func TestRemoteTagsVersionSorted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git required")
	}
	dir := t.TempDir()
	gitTest(t, dir, "init")
	gitTest(t, dir, "config", "user.email", "fixture@example.invalid")
	gitTest(t, dir, "config", "user.name", "Fixture")
	os.WriteFile(filepath.Join(dir, "file"), []byte("test"), 0600)
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-m", "initial")
	gitTest(t, dir, "tag", "v1.9.0")
	gitTest(t, dir, "tag", "-a", "v1.10.0", "-m", "annotated")
	tags, err := remoteTags(context.Background(), dir)
	if err != nil || len(tags) != 2 || tags[0] != "v1.10.0" {
		t.Fatalf("incorrect latest: %v %v", tags, err)
	}
}
func TestNamedInstallationNeverReusesFolder(t *testing.T) {
	c := validConfig(t)
	c.DirectoryName = "existing"
	root := filepath.Join(c.Output, c.DirectoryName)
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "keep"), []byte("preserved"), 0600)
	result := install(context.Background(), Plan{Config: c}, func(Progress) {})
	if result.Err == nil {
		t.Fatal("reused existing folder")
	}
	data, _ := os.ReadFile(filepath.Join(root, "keep"))
	if string(data) != "preserved" {
		t.Fatal("modified existing files")
	}
}
