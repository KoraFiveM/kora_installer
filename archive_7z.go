package main

import (
	"errors"
	"github.com/bodgit/sevenzip"
	"os"
	"strings"
)

func extractWindowsArtifact(file, dest string, emit func(string)) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	magic := make([]byte, 6)
	_, err = f.Read(magic)
	f.Close()
	if err != nil {
		return err
	}
	if string(magic) != "7z\xbc\xaf\x27\x1c" {
		return extractZipProgress(file, dest, false, emit)
	}
	reader, err := sevenzip.OpenReader(file)
	if err != nil {
		return err
	}
	defer reader.Close()
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	seen := map[string]bool{}
	budget := int64(maxExtract)
	for _, entry := range reader.File {
		name, err := archiveName(entry.Name, false)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			return errors.New("duplicate archive entry")
		}
		seen[key] = true
		mode := entry.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return errors.New("unsupported Windows archive entry")
		}
		if mode.IsDir() {
			if err := root.MkdirAll(name, 0700); err != nil {
				return err
			}
			continue
		}
		if entry.UncompressedSize > uint64(budget) {
			return errors.New("archive exceeds extraction limit")
		}
		stream, err := entry.Open()
		if err != nil {
			return err
		}
		err = archiveFile(root, name, stream, int64(entry.UncompressedSize), 0600, &budget)
		stream.Close()
		if err != nil {
			return err
		}
		if emit != nil {
			emit(name)
		}
	}
	return nil
}
