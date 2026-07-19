package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/releasepack"
)

const (
	manifestSchema                = "amsftp-public-release-manifest-v1"
	internalPreviewManifestSchema = "amsftp-internal-preview-manifest-v1"
	maxManifestBytes              = 1 << 20
	maxMaterialBytes              = 1 << 20
	maxBinaryBytes                = 256 << 20
)

type inputManifest struct {
	Schema          string             `json:"schema"`
	Version         string             `json:"version"`
	Commit          string             `json:"commit"`
	Tree            string             `json:"tree"`
	SourceDateEpoch int64              `json:"source_date_epoch"`
	Materials       manifestMaterials  `json:"materials"`
	Platforms       []manifestPlatform `json:"platforms"`
	Modules         []manifestModule   `json:"modules"`
}

type manifestMaterials struct {
	License         string `json:"license"`
	Notice          string `json:"notice"`
	Install         string `json:"install"`
	Uninstall       string `json:"uninstall"`
	Man             string `json:"man"`
	InternalPreview string `json:"internal_preview,omitempty"`
	BashCompletion  string `json:"bash_completion,omitempty"`
	ZshCompletion   string `json:"zsh_completion,omitempty"`
	FishCompletion  string `json:"fish_completion,omitempty"`
}

type manifestPlatform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Path string `json:"path"`
}

type manifestModule struct {
	Path        string                     `json:"path"`
	Version     string                     `json:"version"`
	Sum         string                     `json:"sum"`
	License     string                     `json:"license"`
	Targets     []manifestTarget           `json:"targets"`
	Replacement *manifestModuleReplacement `json:"replacement,omitempty"`
}

type manifestTarget struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type manifestModuleReplacement struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	return runWithInspector(args, stdout, releasepack.InspectGoBinary)
}

type binaryInspector func([]byte) (releasepack.GoBuildEvidence, error)

func runWithInspector(args []string, stdout io.Writer, inspect binaryInspector) error {
	if len(args) != 2 {
		return errors.New("usage: releasepack MANIFEST.json OUTPUT_DIRECTORY")
	}
	manifest, root, err := readManifest(args[0])
	if err != nil {
		return err
	}
	request, err := loadBundleRequest(root, manifest, inspect)
	if err != nil {
		return err
	}
	var bundle releasepack.Bundle
	switch manifest.Schema {
	case manifestSchema:
		bundle, err = releasepack.BuildPublicBundle(request)
	case internalPreviewManifestSchema:
		bundle, err = releasepack.BuildInternalPreviewBundle(request)
	default:
		return errors.New("read release manifest: unsupported schema")
	}
	if err != nil {
		return err
	}
	if err := releasepack.WriteBundle(args[1], bundle); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, args[1])
	return err
}

func readManifest(path string) (inputManifest, string, error) {
	pathInfo, err := os.Lstat(path) //nolint:gosec // the manifest path is the command's explicit user-selected input.
	if err != nil {
		return inputManifest{}, "", fmt.Errorf("read release manifest: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Size() <= 0 || pathInfo.Size() > maxManifestBytes {
		return inputManifest{}, "", errors.New("read release manifest: manifest must be a bounded non-symlink regular file")
	}
	file, err := os.Open(path) //nolint:gosec // Lstat above proves the explicit manifest is a bounded non-symlink regular file.
	if err != nil {
		return inputManifest{}, "", fmt.Errorf("read release manifest: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != pathInfo.Size() {
		return inputManifest{}, "", errors.New("read release manifest: manifest must be a bounded regular file")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest inputManifest
	if err := decoder.Decode(&manifest); err != nil {
		return inputManifest{}, "", fmt.Errorf("read release manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return inputManifest{}, "", errors.New("read release manifest: trailing JSON is forbidden")
	}
	if manifest.Schema != manifestSchema && manifest.Schema != internalPreviewManifestSchema || len(manifest.Modules) == 0 {
		return inputManifest{}, "", errors.New("read release manifest: schema or dependency inventory is invalid")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return inputManifest{}, "", fmt.Errorf("read release manifest: resolve path: %w", err)
	}
	root, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return inputManifest{}, "", fmt.Errorf("read release manifest: resolve root: %w", err)
	}
	return manifest, root, nil
}

func loadBundleRequest(root string, manifest inputManifest, inspect binaryInspector) (releasepack.BundleRequest, error) {
	license, err := readConfinedMaterial(root, manifest.Materials.License)
	if err != nil {
		return releasepack.BundleRequest{}, fmt.Errorf("load LICENSE: %w", err)
	}
	notice, err := readConfinedMaterial(root, manifest.Materials.Notice)
	if err != nil {
		return releasepack.BundleRequest{}, fmt.Errorf("load NOTICE: %w", err)
	}
	install, err := readConfinedMaterial(root, manifest.Materials.Install)
	if err != nil {
		return releasepack.BundleRequest{}, fmt.Errorf("load INSTALL.md: %w", err)
	}
	uninstall, err := readConfinedMaterial(root, manifest.Materials.Uninstall)
	if err != nil {
		return releasepack.BundleRequest{}, fmt.Errorf("load UNINSTALL.md: %w", err)
	}
	man, err := readConfinedMaterial(root, manifest.Materials.Man)
	if err != nil {
		return releasepack.BundleRequest{}, fmt.Errorf("load amsftp.1: %w", err)
	}
	var internalPreview, bashCompletion, zshCompletion, fishCompletion []byte
	if manifest.Schema == internalPreviewManifestSchema {
		internalPreview, err = readConfinedMaterial(root, manifest.Materials.InternalPreview)
		if err != nil {
			return releasepack.BundleRequest{}, fmt.Errorf("load INTERNAL-PREVIEW.md: %w", err)
		}
		bashCompletion, err = readConfinedMaterial(root, manifest.Materials.BashCompletion)
		if err != nil {
			return releasepack.BundleRequest{}, fmt.Errorf("load bash completion: %w", err)
		}
		zshCompletion, err = readConfinedMaterial(root, manifest.Materials.ZshCompletion)
		if err != nil {
			return releasepack.BundleRequest{}, fmt.Errorf("load zsh completion: %w", err)
		}
		fishCompletion, err = readConfinedMaterial(root, manifest.Materials.FishCompletion)
		if err != nil {
			return releasepack.BundleRequest{}, fmt.Errorf("load fish completion: %w", err)
		}
	}

	platforms := make([]releasepack.PlatformBinary, 0, len(manifest.Platforms))
	seenPaths := make(map[string]struct{}, len(manifest.Platforms))
	for _, platform := range manifest.Platforms {
		binary, resolved, err := readConfinedFileWithPath(root, platform.Path, maxBinaryBytes)
		if err != nil {
			return releasepack.BundleRequest{}, fmt.Errorf("load %s/%s binary: %w", platform.OS, platform.Arch, err)
		}
		if _, duplicate := seenPaths[resolved]; duplicate {
			return releasepack.BundleRequest{}, errors.New("load release binaries: each target requires a distinct input file")
		}
		build, err := inspect(binary)
		if err != nil {
			return releasepack.BundleRequest{}, fmt.Errorf("inspect %s/%s binary: %w", platform.OS, platform.Arch, err)
		}
		seenPaths[resolved] = struct{}{}
		state := releasepack.BinaryPublicPreview
		if manifest.Schema == internalPreviewManifestSchema {
			state = releasepack.BinaryInternalPreview
		}
		platforms = append(platforms, releasepack.PlatformBinary{Target: releasepack.Target{OS: platform.OS, Arch: platform.Arch}, Bytes: binary, State: state, Build: &build})
	}
	modules := make([]releasepack.Module, 0, len(manifest.Modules))
	for _, module := range manifest.Modules {
		targets := make([]releasepack.Target, 0, len(module.Targets))
		for _, target := range module.Targets {
			targets = append(targets, releasepack.Target{OS: target.OS, Arch: target.Arch})
		}
		item := releasepack.Module{Path: module.Path, Version: module.Version, Sum: module.Sum, License: module.License, Targets: targets}
		if module.Replacement != nil {
			item.Replacement = &releasepack.ModuleReplacement{Path: module.Replacement.Path, Version: module.Replacement.Version, Sum: module.Replacement.Sum}
		}
		modules = append(modules, item)
	}
	return releasepack.BundleRequest{
		Version: manifest.Version, Commit: manifest.Commit, Tree: manifest.Tree, SourceDateEpoch: manifest.SourceDateEpoch,
		Materials: releasepack.Materials{
			License: license, Notice: notice, Install: install, Uninstall: uninstall, Man: man,
			InternalPreview: internalPreview, BashCompletion: bashCompletion, ZshCompletion: zshCompletion, FishCompletion: fishCompletion,
		},
		Platforms: platforms, Modules: modules,
	}, nil
}

func readConfinedMaterial(root, relative string) ([]byte, error) {
	raw, _, err := readConfinedFileWithPath(root, relative, maxMaterialBytes)
	return raw, err
}

func readConfinedFileWithPath(root, relative string, maximum int64) ([]byte, string, error) {
	if relative == "" || !filepath.IsLocal(relative) || filepath.Clean(relative) != relative {
		return nil, "", errors.New("path must be a canonical relative path below the manifest directory")
	}
	current := root
	for _, component := range splitPath(relative) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current) //nolint:gosec // each canonical relative component is joined below the resolved manifest root.
		if err != nil {
			return nil, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, "", errors.New("symbolic links are forbidden in release inputs")
		}
	}
	info, err := os.Stat(current) //nolint:gosec // the complete path was confined component-by-component and symlinks were rejected.
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, "", errors.New("release input must be a non-empty bounded regular file")
	}
	raw, err := os.ReadFile(current) //nolint:gosec // current is the fully confined non-symlink regular input proven above.
	if err != nil {
		return nil, "", err
	}
	return raw, current, nil
}

func splitPath(path string) []string {
	var result []string
	for path != "." && path != string(filepath.Separator) {
		directory, file := filepath.Split(path)
		if file != "" {
			result = append([]string{file}, result...)
		}
		path = filepath.Clean(directory)
	}
	return result
}
