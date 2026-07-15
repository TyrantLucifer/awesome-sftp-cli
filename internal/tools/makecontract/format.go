package main

import (
	"bytes"
	"fmt"
	goformat "go/format"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func unformattedGoFiles(paths []string) ([]string, error) {
	var files []string
	for _, root := range paths {
		// #nosec G703 -- the CLI restricts roots to the committed cmd and internal directories.
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", root, err)
		}
		if !info.IsDir() {
			if strings.HasSuffix(root, ".go") {
				files = append(files, root)
			}
			continue
		}
		// #nosec G703 -- the CLI restricts roots to the committed cmd and internal directories.
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", root, err)
		}
	}
	sort.Strings(files)

	var unformatted []string
	for _, path := range files {
		// #nosec G304 -- every path was discovered below a fixed, repository-owned root.
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		formatted, err := goformat.Source(source)
		if err != nil {
			return nil, fmt.Errorf("format %s: %w", path, err)
		}
		if !bytes.Equal(source, formatted) {
			unformatted = append(unformatted, path)
		}
	}
	return unformatted, nil
}
