package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const catalogFixture = `{"schemaVersion":1,"resources":[{"id":"kora-core","repository":"https://github.com/KoraFiveM/kora_core.git","latest":"0.1.1","versions":[{"version":"0.1.0","tag":"v0.1.0","commit":"1111111111111111111111111111111111111111"},{"version":"0.1.1","tag":"v0.1.1","commit":"2222222222222222222222222222222222222222"}]}]}`

func TestCatalogFallbackAndCustomIsolation(t *testing.T) {
	primary := "https://docs.test/resources.json"
	fallback := "https://github.test/resources.json"
	for _, mode := range []string{"json", "html", "invalid", "offline"} {
		t.Run(mode, func(t *testing.T) {
			calls := []string{}
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.String())
				body, content := catalogFixture, "application/json; charset=utf-8"
				if r.URL.String() == primary {
					switch mode {
					case "html":
						content = "text/html"
					case "invalid":
						body = "<html>oops</html>"
					case "offline":
						return nil, fmt.Errorf("offline")
					}
				} else {
					content = "text/plain"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {content}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			c, source, err := loadCatalog(context.Background(), client, primary, primary, fallback)
			if err != nil || len(c.Resources) != 1 {
				t.Fatal(err)
			}
			if mode == "json" {
				if source != primary || len(calls) != 1 {
					t.Fatal("unnecessary fallback")
				}
			} else if source != fallback || len(calls) != 2 {
				t.Fatal("missing fallback")
			}
		})
	}
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("offline") })}
	if _, _, err := loadCatalog(context.Background(), client, "http://custom.test/catalog.json", primary, fallback); err == nil || calls != 1 {
		t.Fatal("custom catalog silently replaced")
	}
}
func TestCatalogPinsPublishedLatestAndLocalPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "resources.json")
	os.WriteFile(file, []byte(catalogFixture), 0600)
	c, _, err := loadCatalog(context.Background(), http.DefaultClient, file, defaultCatalogURL, fallbackCatalogURL)
	if err != nil {
		t.Fatal(err)
	}
	base := []Resource{{Path: "[pre]/kora_core", Repository: "https://github.com/KoraFiveM/kora_core.git"}}
	list, err := catalogResources(c, base, "latest", false)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	if list[0].Ref != "v0.1.1" || list[0].Commit != strings.Repeat("2", 40) || list[0].Path != "[pre]/kora_core" {
		t.Fatalf("ignored published latest: %+v", list)
	}
	c.Resources[0].Path = "../escape"
	if _, err := catalogResources(c, base, "latest", false); err == nil {
		t.Fatal("accepted traversal")
	}
}
func TestGitHubCatalogURL(t *testing.T) {
	got, err := catalogURL("https://github.com/KoraFiveM/kora_docs_next/blob/main/catalog/resources.json")
	if err != nil || got != fallbackCatalogURL {
		t.Fatalf("bad GitHub normalization: %s %v", got, err)
	}
}

func TestCatalogMissingVersionFallsBackPerResource(t *testing.T) {
	c := Catalog{SchemaVersion: 1, Resources: []CatalogResource{
		{ID: "kora-core", Repository: "https://github.com/KoraFiveM/kora_core.git", Latest: "0.3.0", Versions: []CatalogRelease{{Version: "0.2.1", Tag: "v0.2.1", Commit: strings.Repeat("1", 40)}, {Version: "0.3.0", Tag: "v0.3.0", Commit: strings.Repeat("2", 40)}}},
		{ID: "kora-crypto", Repository: "https://github.com/KoraFiveM/kora_crypto.git", Latest: "0.1.1", Versions: []CatalogRelease{{Version: "9.0.0", Tag: "v9.0.0", Commit: strings.Repeat("3", 40)}, {Version: "0.1.1", Tag: "v0.1.1", Commit: strings.Repeat("4", 40)}}},
	}}
	for _, policy := range []string{"v0.2.1", "0.2.1"} {
		list, err := catalogResources(c, nil, policy, false)
		if err != nil {
			t.Fatal(err)
		}
		if list[0].Ref != "v0.2.1" || list[0].Commit != strings.Repeat("1", 40) || list[0].SelectionNote != "" {
			t.Fatal("changed available requested release")
		}
		if list[1].Ref != "v0.1.1" || list[1].Commit != strings.Repeat("4", 40) || !strings.Contains(list[1].SelectionNote, policy) {
			t.Fatalf("wrong fallback: %+v", list[1])
		}
	}
	c.Resources[1].Latest = "missing"
	if _, err := catalogResources(c, nil, "v0.2.1", false); err == nil {
		t.Fatal("silently accepted broken latest pointer")
	}
}
