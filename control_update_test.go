package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlUpdateBackupGate(t *testing.T) {
	for _, mode := range []string{"success", "dirty", "dump-fails", "stop-fails", "inside", "ignored-collision"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			upstream := t.TempDir()
			gitTest(t, upstream, "init")
			gitTest(t, upstream, "config", "user.email", "test@example.invalid")
			gitTest(t, upstream, "config", "user.name", "Test")
			os.WriteFile(filepath.Join(upstream, "resource.lua"), []byte("old"), 0600)
			os.WriteFile(filepath.Join(upstream, ".gitignore"), []byte("private.cfg\n"), 0600)
			gitTest(t, upstream, "add", ".")
			gitTest(t, upstream, "commit", "-m", "old")
			old := gitTest(t, upstream, "rev-parse", "HEAD")
			root := t.TempDir()
			live := filepath.Join(root, "resources", "fixture")
			os.Mkdir(filepath.Dir(live), 0700)
			gitTest(t, root, "clone", "--", upstream, live)
			os.WriteFile(filepath.Join(live, "private.cfg"), []byte("private"), 0600)
			os.WriteFile(filepath.Join(root, ".env"), []byte("private configuration"), 0600)
			os.WriteFile(filepath.Join(upstream, "resource.lua"), []byte("new"), 0600)
			if mode == "ignored-collision" {
				os.WriteFile(filepath.Join(upstream, "private.cfg"), []byte("upstream"), 0600)
				gitTest(t, upstream, "add", "-f", "private.cfg")
			}
			gitTest(t, upstream, "commit", "-am", "new")
			target := gitTest(t, upstream, "rev-parse", "HEAD")
			p := UpdatePlan{Server: Server{Path: root}, Resources: []Checkout{{Resource: Resource{Path: "fixture", Repository: upstream, Commit: target}, Current: old}}}
			if mode == "dirty" {
				os.WriteFile(filepath.Join(live, "local.lua"), []byte("local"), 0600)
			}
			parent := t.TempDir()
			if mode == "inside" {
				parent = root
			}
			var events []string
			backup, err := applyControlUpdate(ctx, p, parent, func(Progress) {}, func(context.Context, string) error {
				events = append(events, "stop")
				if mode == "stop-fails" {
					return errors.New("stop failed")
				}
				return nil
			}, func(_ context.Context, server, dir string) error {
				events = append(events, "dump")
				if mode == "dump-fails" {
					return errors.New("dump failed")
				}
				if !regularFile(filepath.Join(dir, "resources", "fixture", ".git", "HEAD")) {
					t.Fatal("backup missing git")
				}
				if got := gitTest(t, live, "rev-parse", "HEAD"); got != old {
					t.Fatal("changed before dump")
				}
				return os.WriteFile(filepath.Join(dir, "database.sql"), []byte("fixture"), 0600)
			})
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if strings.Join(events, ",") != "stop,dump" {
					t.Fatal(events)
				}
				if gitTest(t, live, "rev-parse", "HEAD") != target {
					t.Fatal("not updated")
				}
				if !regularFile(filepath.Join(backup, "backup.complete")) {
					t.Fatal("backup incomplete")
				}
			} else {
				if err == nil {
					t.Fatal("expected refusal")
				}
				if gitTest(t, live, "rev-parse", "HEAD") != old {
					t.Fatal("live resource changed")
				}
			}
			if (mode == "dirty" || mode == "inside") && len(events) != 0 {
				t.Fatal("stopped before preflight")
			}
			data, _ := os.ReadFile(filepath.Join(live, "private.cfg"))
			if string(data) != "private" {
				t.Fatal("ignored file lost")
			}
		})
	}
}

func TestBackupRejectsSymlinks(t *testing.T) {
	source := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(source, "link")); err != nil {
		t.Skip(err)
	}
	if err := copyBackupTree(context.Background(), source, filepath.Join(t.TempDir(), "backup")); err == nil {
		t.Fatal("followed symlink")
	}
}
