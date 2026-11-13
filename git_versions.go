package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func remoteGit(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := backgroundCommand(exec.CommandContext(ctx, "git", append([]string{"ls-remote"}, args...)...))
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("Git could not read remote refs; check Git credentials and repository access")
	}
	return string(out), nil
}
func remoteTags(ctx context.Context, repo string) ([]string, error) {
	out, err := remoteGit(ctx, "--tags", "--refs", "--sort=-version:refname", repo)
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.HasPrefix(fields[1], "refs/tags/") {
			tags = append(tags, strings.TrimPrefix(fields[1], "refs/tags/"))
		}
	}
	return tags, nil
}
func resolveGit(ctx context.Context, r Resource, policy string) (Resource, error) {
	slug, err := repoSlug(r.Repository)
	if err != nil {
		return r, err
	}
	ref := r.Ref
	if ref == "" && strings.HasPrefix(strings.ToLower(slug), "korafivem/") {
		ref = policy
	}
	if ref == "latest" {
		tags, err := remoteTags(ctx, r.Repository)
		if err != nil {
			return r, err
		}
		ref = ""
		if len(tags) > 0 {
			ref = tags[0]
		}
	}
	if ref == "" || ref == "default" {
		out, err := remoteGit(ctx, "--symref", r.Repository, "HEAD")
		if err != nil {
			return r, err
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) == 3 && f[0] == "ref:" {
				r.Ref = strings.TrimPrefix(f[1], "refs/heads/")
			}
			if len(f) == 2 && f[1] == "HEAD" {
				r.Commit = f[0]
			}
		}
		if len(r.Commit) != 40 || r.Ref == "" {
			return r, fmt.Errorf("remote default branch unavailable")
		}
		return r, nil
	}
	out, err := remoteGit(ctx, r.Repository, "refs/tags/"+ref, "refs/tags/"+ref+"^{}", "refs/heads/"+ref)
	if err != nil {
		return r, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			refs[f[1]] = f[0]
		}
	}
	for _, key := range []string{"refs/tags/" + ref + "^{}", "refs/tags/" + ref, "refs/heads/" + ref} {
		if sha := refs[key]; sha != "" {
			r.Ref, r.Commit = ref, sha
			return r, nil
		}
	}
	return r, fmt.Errorf("ref %q does not exist in %s", ref, slug)
}
