package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed assets/resources.manifest
var manifest string

const buildsURL = "https://raw.githubusercontent.com/citizenfx/fivem-docs/master/content/docs/server-manual/server-commands.md"
const artifactAPI = "https://artifacts.jgscripts.com/json"

var tokenOnce sync.Once
var cachedToken string

// Reuse an existing GitHub CLI login without printing or persisting its token.
func githubToken() string {
	tokenOnce.Do(func() {
		cachedToken = os.Getenv("GITHUB_TOKEN")
		if cachedToken == "" {
			cachedToken = os.Getenv("GH_TOKEN")
		}
		if cachedToken == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if data, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
				cachedToken = strings.TrimSpace(string(data))
			}
		}
	})
	return cachedToken
}

type Sources struct {
	Client *http.Client
	API    string
}

func newSources() Sources {
	return Sources{Client: &http.Client{Timeout: 45 * time.Second}, API: "https://api.github.com"}
}
func (s Sources) get(ctx context.Context, address string, limit int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "KoraFramework-installer")
	if req.URL.Host == "api.github.com" {
		if token := githubToken(); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, e := s.Client.Do(req)
	if e != nil {
		return nil, errors.New("network request failed; check connectivity and retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("upstream returned HTTP %d for %s (GitHub private repositories require Git credentials and GITHUB_TOKEN)", resp.StatusCode, req.URL.Host)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("upstream response exceeds size limit")
	}
	return b, e
}
func (s Sources) json(ctx context.Context, address string, v any) error {
	b, e := s.get(ctx, address, 4<<20)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func parseBuilds(b []byte) ([]int, error) {
	text := string(b)
	start := strings.Index(text, "**FiveM builds**")
	end := strings.Index(text, "**RedM builds**")
	if start < 0 || end < start {
		return nil, errors.New("FiveM build list format changed; supply --game-build")
	}
	matches := regexp.MustCompile(`(?m)^\|\s*([0-9]+)\s*\|`).FindAllStringSubmatch(text[start:end], -1)
	builds := []int{}
	for _, m := range matches {
		v, _ := strconv.Atoi(m[1])
		if v > 1 {
			builds = append(builds, v)
		}
	}
	if len(builds) == 0 {
		return nil, errors.New("FiveM build list was empty")
	}
	sort.Sort(sort.Reverse(sort.IntSlice(builds)))
	return builds, nil
}
func (s Sources) builds(ctx context.Context) ([]int, error) {
	b, e := s.get(ctx, buildsURL, 2<<20)
	if e != nil {
		return nil, e
	}
	return parseBuilds(b)
}
func artifactURL(address, platform string) error {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Host != "runtime.fivem.net" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("artifact must use an official HTTPS runtime.fivem.net URL")
	}
	prefix, suffix := "/artifacts/fivem/build_proot_linux/master/", "/fx.tar.xz"
	if platform == "windows" {
		prefix, suffix = "/artifacts/fivem/build_server_windows/master/", "/server.zip"
	}
	if !strings.HasPrefix(u.Path, prefix) || !strings.HasSuffix(u.Path, suffix) || strings.Contains(u.Path, "..") {
		return errors.New("artifact URL does not match target platform")
	}
	return nil
}
func (s Sources) artifact(ctx context.Context, choice, platform string) (string, error) {
	if choice != "recommended" {
		return choice, artifactURL(choice, platform)
	}
	var a struct {
		Linux   string `json:"linuxDownloadLink"`
		Windows string `json:"windowsDownloadLink"`
	}
	if e := s.json(ctx, artifactAPI, &a); e != nil {
		return "", e
	}
	address := a.Linux
	if platform == "windows" {
		address = a.Windows
	}
	return address, artifactURL(address, platform)
}
func repoSlug(address string) (string, error) {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("resources require a credential-free https://github.com/owner/repository URL")
	}
	slug := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(slug) || strings.Contains(slug, "..") {
		return "", errors.New("invalid GitHub repository")
	}
	return slug, nil
}
func resources(r Recipe) ([]Resource, error) {
	list := []Resource{}
	scan := bufio.NewScanner(strings.NewReader(manifest))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Fields(line)
		if len(p) != 2 {
			return nil, errors.New("invalid embedded manifest")
		}
		parts := strings.Split(strings.TrimPrefix(p[0], "resources/"), "/")
		parts[0] = "[" + parts[0] + "]"
		list = append(list, Resource{Path: strings.Join(parts, "/"), Repository: p[1]})
	}
	if r.Replace {
		list = nil
	}
	seen := map[string]bool{}
	for _, v := range r.Resources {
		if seen[strings.ToLower(v.Path)] {
			return nil, errors.New("duplicate template path")
		}
		seen[strings.ToLower(v.Path)] = true
		found := false
		for i := range list {
			if list[i].Path == v.Path {
				list[i] = v
				found = true
				break
			}
		}
		if !found {
			list = append(list, v)
		}
	}
	for i, v := range list {
		if !relativePath(v.Path) {
			return nil, fmt.Errorf("unsafe resource path %q", v.Path)
		}
		for _, segment := range strings.Split(v.Path, "/") {
			if !resourceName.MatchString(segment) || segment == ".git" {
				return nil, errors.New("invalid resource path")
			}
		}
		if _, e := repoSlug(v.Repository); e != nil {
			return nil, e
		}
		if strings.HasPrefix(v.Ref, "-") || !safeText(v.Ref) {
			return nil, errors.New("invalid resource ref")
		}
		for j := 0; j < i; j++ {
			a, b := strings.ToLower(v.Path), strings.ToLower(list[j].Path)
			if a == b || (!r.Replace && (seen[a] || seen[b]) && (strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/"))) {
				return nil, errors.New("overlapping resource paths")
			}
		}
	}
	return list, scan.Err()
}
func (s Sources) tags(ctx context.Context, slug string) ([]string, error) {
	if s.API == "https://api.github.com" {
		if _, err := exec.LookPath("git"); err == nil {
			return remoteTags(ctx, "https://github.com/"+slug+".git")
		}
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if e := s.json(ctx, s.API+"/repos/"+slug+"/tags?per_page=100", &tags); e != nil {
		return nil, e
	}
	out := []string{}
	for _, t := range tags {
		if safeText(t.Name) {
			out = append(out, t.Name)
		}
	}
	return out, nil
}
func (s Sources) resolve(ctx context.Context, r Resource, tag string) (Resource, error) {
	if s.API == "https://api.github.com" && !regexp.MustCompile(`^[a-fA-F0-9]{40}$`).MatchString(r.Ref) {
		if _, err := exec.LookPath("git"); err == nil {
			return resolveGit(ctx, r, tag)
		}
	}
	slug, e := repoSlug(r.Repository)
	if e != nil {
		return r, e
	}
	ref := r.Ref
	if ref == "" && strings.HasPrefix(strings.ToLower(slug), "korafivem/") {
		ref = tag
	}
	if ref == "latest" {
		tags, e := s.tags(ctx, slug)
		if e != nil {
			return r, e
		}
		ref = ""
		if len(tags) > 0 {
			ref = tags[0]
		}
	}
	if ref == "" || ref == "default" {
		var repo struct {
			DefaultBranch string `json:"default_branch"`
		}
		if e := s.json(ctx, s.API+"/repos/"+slug, &repo); e != nil {
			return r, e
		}
		ref = repo.DefaultBranch
	}
	if ref == "" {
		return r, errors.New("repository has no default branch")
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if e := s.json(ctx, s.API+"/repos/"+slug+"/commits/"+url.PathEscape(ref), &commit); e != nil {
		return r, e
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(commit.SHA) {
		return r, errors.New("GitHub returned an invalid commit")
	}
	r.Ref = ref
	r.Commit = commit.SHA
	return r, nil
}
