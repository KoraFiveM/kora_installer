package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
)

const defaultCatalogURL = "https://docs.koraframe.work/resources.json"
const fallbackCatalogURL = "https://raw.githubusercontent.com/KoraFiveM/kora_docs_next/main/catalog/resources.json"

type CatalogRelease struct {
	Version string `json:"version"`
	Tag     string `json:"tag"`
	Commit  string `json:"commit"`
}
type CatalogResource struct {
	ID         string           `json:"id"`
	Repository string           `json:"repository"`
	Path       string           `json:"path"`
	Latest     string           `json:"latest"`
	Versions   []CatalogRelease `json:"versions"`
}
type Catalog struct {
	SchemaVersion int               `json:"schemaVersion"`
	Resources     []CatalogResource `json:"resources"`
}

func catalogURL(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("catalog URL must be HTTP(S), without credentials or a fragment")
	}
	if u.Host == "github.com" {
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		if len(parts) < 5 || parts[2] != "blob" {
			return "", errors.New("use a GitHub file/blob URL")
		}
		u.Host = "raw.githubusercontent.com"
		u.Path = "/" + strings.Join(append(parts[:2], parts[3:]...), "/")
	}
	return u.String(), nil
}
func readCatalog(ctx context.Context, client *http.Client, source string, requireJSONHeader bool) (Catalog, error) {
	var reader io.Reader
	var closer io.Closer
	if strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "http://") {
		address, err := catalogURL(source)
		if err != nil {
			return Catalog{}, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
		if err != nil {
			return Catalog{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "KoraFramework-installer")
		response, err := client.Do(req)
		if err != nil {
			return Catalog{}, errors.New("catalog request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return Catalog{}, fmt.Errorf("catalog returned HTTP %d", response.StatusCode)
		}
		if requireJSONHeader {
			media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
			if err != nil || (media != "application/json" && !strings.HasSuffix(media, "+json")) {
				return Catalog{}, errors.New("catalog did not return a JSON Content-Type")
			}
		}
		reader = response.Body
	} else {
		f, err := os.Open(source)
		if err != nil {
			return Catalog{}, err
		}
		reader = f
		closer = f
		defer closer.Close()
	}
	data, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
	if err != nil {
		return Catalog{}, err
	}
	if len(data) > 4<<20 {
		return Catalog{}, errors.New("catalog exceeds 4 MiB")
	}
	var c Catalog
	if err = json.Unmarshal(data, &c); err != nil {
		return c, errors.New("catalog body is not valid resources.json")
	}
	if c.SchemaVersion != 1 || len(c.Resources) == 0 || len(c.Resources) > 500 {
		return c, errors.New("catalog requires schemaVersion 1 and 1–500 resources")
	}
	seen := map[string]bool{}
	for _, r := range c.Resources {
		if !resourceName.MatchString(r.ID) || seen[strings.ToLower(r.ID)] {
			return c, errors.New("invalid or duplicate catalog resource id")
		}
		seen[strings.ToLower(r.ID)] = true
		if _, err := repoSlug(r.Repository); err != nil {
			return c, err
		}
		found := false
		versions := map[string]bool{}
		for _, v := range r.Versions {
			if v.Version == "" || versions[v.Version] || !regexp.MustCompile(`^[a-fA-F0-9]{40}$`).MatchString(v.Commit) || v.Tag == "" || !safeText(v.Tag) || strings.HasPrefix(v.Tag, "-") {
				return c, fmt.Errorf("invalid release entry for %s", r.ID)
			}
			versions[v.Version] = true
			if v.Version == r.Latest {
				found = true
			}
		}
		if !found {
			return c, fmt.Errorf("latest release missing for %s", r.ID)
		}
	}
	return c, nil
}
func loadCatalog(ctx context.Context, client *http.Client, source, primary, fallback string) (Catalog, string, error) {
	address := source
	if address == "" {
		address = primary
	}
	isDefault := address == primary
	// GitHub raw deliberately serves text/plain; validate its JSON body instead.
	u, _ := url.Parse(address)
	header := u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "github.com" && u.Host != "raw.githubusercontent.com"
	c, err := readCatalog(ctx, client, address, header)
	if err == nil {
		return c, address, nil
	}
	if !isDefault {
		return c, address, err
	}
	c, fallbackErr := readCatalog(ctx, client, fallback, false)
	if fallbackErr != nil {
		return c, fallback, fmt.Errorf("primary catalog failed (%v); GitHub fallback failed (%w)", err, fallbackErr)
	}
	return c, fallback, nil
}
func catalogResources(c Catalog, base []Resource, policy string, dependencies bool) ([]Resource, error) {
	paths := map[string]string{}
	catalogRepos := map[string]bool{}
	for _, r := range base {
		paths[strings.ToLower(strings.TrimSuffix(r.Repository, ".git"))] = r.Path
	}
	for _, r := range c.Resources {
		catalogRepos[strings.ToLower(strings.TrimSuffix(r.Repository, ".git"))] = true
	}
	var list []Resource
	if dependencies {
		for _, r := range base {
			key := strings.ToLower(strings.TrimSuffix(r.Repository, ".git"))
			name := path.Base(key)
			if !catalogRepos[key] && !strings.HasPrefix(name, "kora_") {
				list = append(list, r)
			}
		}
	}
	for _, r := range c.Resources {
		dest := r.Path
		if dest == "" {
			dest = paths[strings.ToLower(strings.TrimSuffix(r.Repository, ".git"))]
		}
		if dest == "" {
			dest = "[kora]/" + strings.TrimSuffix(path.Base(r.Repository), ".git")
		}
		chosen := r.Latest
		if policy != "latest" && policy != "" {
			chosen = policy
		}
		found := false
		for _, v := range r.Versions {
			if v.Version == chosen || v.Tag == chosen {
				list = append(list, Resource{Path: dest, Repository: r.Repository, Ref: v.Tag, Commit: strings.ToLower(v.Commit)})
				found = true
				break
			}
		}
		if !found {
			for _, v := range r.Versions {
				if v.Version == r.Latest {
					list = append(list, Resource{Path: dest, Repository: r.Repository, Ref: v.Tag, Commit: strings.ToLower(v.Commit), SelectionNote: fmt.Sprintf("%s unavailable; using published latest %s", chosen, v.Tag)})
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("catalog latest release missing for %s", r.ID)
			}
		}
	}
	return resources(Recipe{Version: 1, Replace: true, Resources: list})
}
