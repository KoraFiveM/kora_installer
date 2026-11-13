package main

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"github.com/ulikunitz/xz"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

const maxDownload int64 = 2 << 30
const maxExtract int64 = 8 << 30

func download(ctx context.Context, address, dest string) error {
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "KoraFramework-installer")
	if req.URL.Host == "api.github.com" && githubToken() != "" {
		req.Header.Set("Authorization", "Bearer "+githubToken())
	}
	client := &http.Client{Timeout: 20 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 8 || r.URL.Scheme != "https" {
			return errors.New("unsafe download redirect")
		}
		if r.URL.Host != "api.github.com" {
			r.Header.Del("Authorization")
		}
		return nil
	}}
	resp, e := client.Do(req)
	if e != nil {
		return errors.New("download failed; check connectivity")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	f, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(f, io.LimitReader(resp.Body, maxDownload+1))
	closeErr := f.Close()
	if e != nil {
		return e
	}
	info, e := os.Stat(dest)
	if e != nil {
		return e
	}
	if info.Size() > maxDownload {
		return errors.New("download exceeds 2 GiB limit")
	}
	return closeErr
}
func archiveName(name string, strip bool) (string, error) {
	name = strings.TrimSuffix(name, "/")
	if strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	if name == "" || name == "." {
		return "", nil
	}
	if !relativePath(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	if strip {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 1 {
			return "", nil
		}
		name = parts[1]
	}
	// Repository archives cannot inject Git config/hooks.
	for _, p := range strings.Split(name, "/") {
		if strings.EqualFold(p, ".git") {
			return "", errors.New("archive contains Git metadata")
		}
	}
	return name, nil
}
func archiveFile(root *os.Root, name string, reader io.Reader, size int64, mode os.FileMode, budget *int64) error {
	if size < 0 || size > *budget {
		return errors.New("archive exceeds extraction limit")
	}
	*budget -= size
	if e := root.MkdirAll(path.Dir(name), 0700); e != nil {
		return e
	}
	f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()&0777)
	if e != nil {
		return e
	}
	n, e := io.Copy(f, io.LimitReader(reader, size+1))
	ce := f.Close()
	if e != nil {
		return e
	}
	if n != size {
		return errors.New("archive entry size mismatch")
	}
	return ce
}
func extractZip(file, dest string, strip bool) error {
	return extractZipProgress(file, dest, strip, nil)
}
func extractZipProgress(file, dest string, strip bool, emit func(string)) error {
	z, e := zip.OpenReader(file)
	if e != nil {
		return e
	}
	defer z.Close()
	root, e := os.OpenRoot(dest)
	if e != nil {
		return e
	}
	defer root.Close()
	budget := maxExtract
	for _, f := range z.File {
		n, e := archiveName(f.Name, strip)
		if e != nil {
			return e
		}
		if n == "" {
			continue
		}
		if emit != nil {
			emit(n)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlinks in ZIP archives are not supported")
		}
		if f.FileInfo().IsDir() {
			if e = root.MkdirAll(n, 0700); e != nil {
				return e
			}
			continue
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		e = archiveFile(root, n, r, int64(f.UncompressedSize64), f.Mode(), &budget)
		r.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
func extractTarXZ(file, dest string) error {
	return extractTarXZProgress(file, dest, nil)
}
func extractTarXZProgress(file, dest string, emit func(string)) error {
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	xzReader, e := xz.NewReader(f)
	if e != nil {
		return e
	}
	t := tar.NewReader(xzReader)
	root, e := os.OpenRoot(dest)
	if e != nil {
		return e
	}
	defer root.Close()
	budget := maxExtract
	type link struct{ name, target string }
	links := []link{}
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		n, e := archiveName(h.Name, false)
		if e != nil {
			return e
		}
		if n == "" {
			continue
		}
		if emit != nil {
			emit(n)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			e = root.MkdirAll(n, 0700)
		case tar.TypeReg, tar.TypeRegA:
			e = archiveFile(root, n, t, h.Size, os.FileMode(h.Mode), &budget)
		case tar.TypeSymlink:
			target := path.Clean(path.Join(path.Dir(n), h.Linkname))
			if strings.HasPrefix(h.Linkname, "/") || strings.Contains(h.Linkname, "\\") || !relativePath(target) {
				return errors.New("archive symlink escapes installation")
			}
			links = append(links, link{n, h.Linkname})
		default:
			return fmt.Errorf("unsupported artifact archive entry type %d", h.Typeflag)
		}
		if e != nil {
			return e
		}
	}
	// Links are created last, so no file can be written through an archive symlink.
	for _, l := range links {
		if e = root.MkdirAll(path.Dir(l.name), 0700); e != nil {
			return e
		}
		if e = root.Symlink(l.target, l.name); e != nil {
			return e
		}
	}
	return nil
}

// Docker extracts inside its Linux image, avoiding Windows symlink privileges.
// Validate the full archive first so tar cannot write through links or paths.
func validateTarXZ(file string) error {
	return validateTarXZProgress(file, nil)
}
func validateTarXZProgress(file string, emit func(string)) error {
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	x, e := xz.NewReader(f)
	if e != nil {
		return e
	}
	reader := tar.NewReader(x)
	entries := map[string]byte{}
	links := map[string]bool{}
	budget := maxExtract
	hasRun := false
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		name, e := archiveName(h.Name, false)
		if e != nil {
			return e
		}
		if name == "" {
			continue
		}
		if emit != nil {
			emit(name)
		}
		if _, exists := entries[name]; exists {
			return errors.New("duplicate artifact entry")
		}
		entries[name] = h.Typeflag
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || h.Size > budget {
				return errors.New("artifact exceeds extraction limit")
			}
			budget -= h.Size
			if name == "run.sh" {
				hasRun = true
			}
		case tar.TypeSymlink:
			target := path.Clean(path.Join(path.Dir(name), h.Linkname))
			if strings.Contains(h.Linkname, "\\") || strings.ContainsRune(h.Linkname, 0) || (!path.IsAbs(h.Linkname) && !relativePath(target)) {
				return errors.New("unsafe artifact link")
			}
			links[name] = true
		default:
			return errors.New("unsupported artifact entry")
		}
	}
	for name := range entries {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if links[parent] {
				return errors.New("artifact writes through a symlink")
			}
		}
	}
	if !hasRun {
		return errors.New("artifact is missing run.sh")
	}
	return nil
}
