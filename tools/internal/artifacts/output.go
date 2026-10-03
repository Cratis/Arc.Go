// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const manifestName = ".arc-gen-manifest.json"
const journalName = ".arc-gen-pending.json"
const outputFormat = 1

type ownedOutput struct {
	Path    string
	Content []byte
}
type ownedEntry struct {
	Root string `json:"root"`
	Path string `json:"path"`
	Hash string `json:"hash"`
}
type ownedManifest struct {
	Format      int          `json:"format"`
	Owner       string       `json:"owner"`
	Scope       string       `json:"scope"`
	Fingerprint string       `json:"fingerprint"`
	Files       []ownedEntry `json:"files"`
}
type pendingChange struct {
	Entry  ownedEntry `json:"entry"`
	Before []byte     `json:"before"`
	After  []byte     `json:"after"`
}
type pendingPlan struct {
	Format  int             `json:"format"`
	Before  []byte          `json:"before"`
	After   []byte          `json:"after"`
	Changes []pendingChange `json:"changes"`
}

func contentHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// A nil journal side denotes absence, not an existing empty file.
func matchesOutput(data []byte, exists bool, expected []byte) bool {
	return exists == (expected != nil) && bytes.Equal(data, expected)
}

// safeOutputPath checks every existing ancestor, including stale destinations.
// The output roots must be in a trusted, exclusively controlled workspace. These
// checks refuse links. Mutations additionally use os.Root to contain concurrent
// symlink replacement; the source workspace still requires exclusive ownership.
func safeOutputPath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s: symlink output or ancestor", current)
			}
			if current != absolute && !info.IsDir() {
				return fmt.Errorf("%s: unsafe output ancestor", current)
			}
		}
		if filepath.Dir(current) == current {
			break
		}
		items, err := os.ReadDir(filepath.Dir(current))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, item := range items {
			if strings.EqualFold(item.Name(), filepath.Base(current)) && item.Name() != filepath.Base(current) {
				return fmt.Errorf("%s: case-colliding output path", current)
			}
		}
	}
	return nil
}

func decodeOwned(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("trailing ownership data")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("trailing ownership data")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func publishOwned(ctx context.Context, moduleRoot, tsRoot string, profile ApplicationProfile, graph *Graph, tags string, outputs []ownedOutput, check bool, fail func(string, string) error) (result error) {
	// go/packages can retain the OS's /var alias for the temporary module. Resolve
	// that trusted source-module spelling once; never resolve output symlinks.
	original, err := filepath.Abs(moduleRoot)
	if err != nil {
		return err
	}
	moduleRoot, err = filepath.EvalSymlinks(original)
	if err != nil {
		return err
	}
	normalize := func(path string) (string, error) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(original, absolute)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.Join(moduleRoot, relative), nil
		}
		return absolute, nil
	}
	tsRoot, err = normalize(tsRoot)
	if err != nil {
		return err
	}
	if tsRoot == moduleRoot {
		return fmt.Errorf("TypeScript output root must not be the module root")
	}
	if err := safeOutputPath(tsRoot); err != nil {
		return err
	}
	packages := make([]string, 0, len(graph.Packages))
	for _, pkg := range graph.Packages {
		packages = append(packages, pkg.GoPath)
	}
	sort.Strings(packages)
	scopeData, err := json.Marshal(struct {
		Packages []string
		Tags     string
	}{packages, tags})
	if err != nil {
		return err
	}
	next := ownedManifest{Format: outputFormat, Owner: profile.Name, Scope: contentHash(scopeData), Fingerprint: graph.Fingerprint, Files: []ownedEntry{}}
	roots := map[string]string{"go": moduleRoot, "ts": tsRoot}
	destination := func(entry ownedEntry) (string, error) {
		root, ok := roots[entry.Root]
		if !ok || entry.Path == "" || !safeRelative(entry.Path) || entry.Path == manifestName || entry.Path == journalName {
			return "", fmt.Errorf("unsafe owned path %q", entry.Path)
		}
		if entry.Root == "go" && filepath.Base(entry.Path) != Filename {
			return "", fmt.Errorf("unsafe Go owned path %q", entry.Path)
		}
		if entry.Root == "ts" && !strings.HasSuffix(entry.Path, ".ts") {
			return "", fmt.Errorf("unsafe TypeScript owned path %q", entry.Path)
		}
		path := filepath.Join(root, filepath.FromSlash(entry.Path))
		return path, safeOutputPath(path)
	}
	key := func(entry ownedEntry) string { return entry.Root + ":" + entry.Path }
	planned := map[string][]byte{}
	entries := map[string]ownedEntry{}
	physical := map[string]string{}
	for _, output := range outputs {
		path, err := normalize(output.Path)
		if err != nil {
			return err
		}
		rootKind := "ts"
		if filepath.Base(path) == Filename {
			rootKind = "go"
		}
		relative, err := filepath.Rel(roots[rootKind], path)
		if err != nil {
			return err
		}
		entry := ownedEntry{Root: rootKind, Path: filepath.ToSlash(relative), Hash: contentHash(output.Content)}
		actual, err := destination(entry)
		if err != nil {
			return err
		}
		folded := strings.ToLower(actual)
		if prior, exists := physical[folded]; exists {
			return fmt.Errorf("case/output collision: %s and %s", prior, actual)
		}
		physical[folded] = actual
		entries[key(entry)] = entry
		if output.Content == nil {
			continue
		}
		if !owned(output.Content) {
			return fmt.Errorf("%s: missing generated ownership marker", actual)
		}
		planned[key(entry)] = output.Content
		entries[key(entry)] = entry
		next.Files = append(next.Files, entry)
	}
	sort.Slice(next.Files, func(i, j int) bool { return key(next.Files[i]) < key(next.Files[j]) })
	manifestPath, journalPath := filepath.Join(tsRoot, manifestName), filepath.Join(tsRoot, journalName)
	for _, path := range []string{manifestPath, journalPath} {
		if err := safeOutputPath(path); err != nil {
			return err
		}
	}
	previous, manifestExists, err := readOutput(manifestPath)
	if err != nil {
		return err
	}
	parseManifest := func(data []byte) (ownedManifest, error) {
		old := ownedManifest{Files: []ownedEntry{}}
		if len(data) == 0 {
			return old, nil
		}
		if err := decodeOwned(data, &old); err != nil {
			return old, fmt.Errorf("invalid ownership manifest: %w", err)
		}
		if old.Format != outputFormat || old.Owner != next.Owner || old.Scope != next.Scope {
			return old, fmt.Errorf("output root belongs to a different profile/package/build scope")
		}
		seen := map[string]bool{}
		for _, entry := range old.Files {
			path, err := destination(entry)
			if err != nil {
				return old, err
			}
			folded := strings.ToLower(path)
			if seen[folded] || len(entry.Hash) != 64 {
				return old, fmt.Errorf("invalid or duplicate manifest entry %q", entry.Path)
			}
			seen[folded] = true
		}
		return old, nil
	}
	pendingBytes, pendingExists, err := readOutput(journalPath)
	if err != nil {
		return err
	}
	var pending pendingPlan
	virtual := map[string][]byte{}
	if pendingExists {
		if check {
			return fmt.Errorf("pending publication requires reconciliation; check mode never repairs it")
		}
		if err := decodeOwned(pendingBytes, &pending); err != nil {
			return fmt.Errorf("invalid pending publication: %w", err)
		}
		if pending.Format != outputFormat {
			return fmt.Errorf("unsupported pending publication")
		}
		before, err := parseManifest(pending.Before)
		if err != nil {
			return err
		}
		after, err := parseManifest(pending.After)
		if err != nil {
			return err
		}
		if !matchesOutput(previous, manifestExists, pending.Before) && !matchesOutput(previous, manifestExists, pending.After) {
			return fmt.Errorf("manifest changed during pending publication")
		}
		hashesBefore, hashesAfter := map[string]string{}, map[string]string{}
		for _, entry := range before.Files {
			hashesBefore[key(entry)] = entry.Hash
		}
		for _, entry := range after.Files {
			hashesAfter[key(entry)] = entry.Hash
		}
		for _, change := range pending.Changes {
			path, err := destination(change.Entry)
			if err != nil {
				return err
			}
			identity := key(change.Entry)
			if _, duplicate := virtual[identity]; duplicate {
				return fmt.Errorf("duplicate pending path")
			}
			for _, side := range []struct {
				data []byte
				hash string
			}{{change.Before, hashesBefore[identity]}, {change.After, hashesAfter[identity]}} {
				if side.data == nil && side.hash == "" {
					continue
				}
				if !owned(side.data) || contentHash(side.data) != side.hash {
					return fmt.Errorf("pending ownership/hash mismatch: %s", path)
				}
			}
			current, exists, err := readOutput(path)
			if err != nil {
				return err
			}
			if !matchesOutput(current, exists, change.Before) && !matchesOutput(current, exists, change.After) {
				return fmt.Errorf("%s: edited during pending publication; preserve journal and restore owned bytes", path)
			}
			virtual[identity] = change.Before
		}
		previous = pending.Before
	}
	old, err := parseManifest(previous)
	if err != nil {
		return err
	}
	oldEntries := map[string]ownedEntry{}
	for _, entry := range old.Files {
		oldEntries[key(entry)] = entry
		entries[key(entry)] = entry
	}
	if err := filepath.WalkDir(tsRoot, func(path string, item fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) && path == tsRoot {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s: symlink in output inventory", path)
		}
		if item.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		relative, err := filepath.Rel(tsRoot, path)
		if err != nil {
			return err
		}
		identity := "ts:" + filepath.ToSlash(relative)
		if _, known := entries[identity]; known {
			return nil
		}
		data, _, err := readOutput(path)
		if err != nil {
			return err
		}
		if owned(data) {
			return fmt.Errorf("%s: generated inventory has no manifest ownership", path)
		}
		return nil
	}); err != nil {
		return err
	}
	identities := make([]string, 0, len(entries))
	for identity := range entries {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	changes := []pendingChange{}
	differences := []string{}
	for _, identity := range identities {
		entry := entries[identity]
		path, err := destination(entry)
		if err != nil {
			return err
		}
		current, exists, err := readOutput(path)
		if err != nil {
			return err
		}
		if data, overridden := virtual[identity]; overridden {
			current = data
			exists = data != nil
		}
		expected, wasOwned := oldEntries[identity]
		if exists {
			if !owned(current) || wasOwned && contentHash(current) != expected.Hash || !wasOwned && !bytes.Equal(current, planned[identity]) {
				return fmt.Errorf("%s: unowned or modified generated output", path)
			}
		}
		desired := planned[identity]
		if bytes.Equal(current, desired) {
			continue
		}
		label := "changed"
		if !exists {
			label = "missing"
		} else if desired == nil {
			label = "stale"
		}
		differences = append(differences, label+": "+path)
		changes = append(changes, pendingChange{Entry: entry, Before: current, After: desired})
	}
	nextBytes, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	nextBytes = append(nextBytes, '\n')
	if check {
		if !bytes.Equal(previous, nextBytes) {
			differences = append(differences, "changed: "+manifestPath)
		}
		if len(differences) > 0 {
			sort.Strings(differences)
			return fmt.Errorf("generated output differs:\n%s", strings.Join(differences, "\n"))
		}
		return nil
	}
	if !pendingExists && len(changes) == 0 && bytes.Equal(previous, nextBytes) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	goRoot, err := openOutputRoot(moduleRoot)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, goRoot.Close()) }()
	tsHandle, err := openOutputRoot(tsRoot)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, tsHandle.Close()) }()
	handles := map[string]*os.Root{"go": goRoot, "ts": tsHandle}
	apply := func(entry ownedEntry, data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := destination(entry)
		if err != nil {
			return err
		}
		operation := "write"
		if data == nil {
			operation = "delete"
		}
		if fail != nil {
			if err := fail(operation, path); err != nil {
				return err
			}
		}
		if data == nil {
			if err := safeOutputPath(path); err != nil {
				return err
			}
			err = handles[entry.Root].Remove(filepath.FromSlash(entry.Path))
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := handles[entry.Root].MkdirAll(filepath.Dir(filepath.FromSlash(entry.Path)), 0755); err != nil {
			return err
		}
		if fail != nil {
			if err := fail("rename", path); err != nil {
				return err
			}
		}
		if err := safeOutputPath(path); err != nil {
			return err
		}
		return writeRootOutput(handles[entry.Root], filepath.FromSlash(entry.Path), data)
	}
	if pendingExists {
		// Recover by rollback. Every live byte was checked before any mutation. A
		// failed rollback retains the same journal and is safely repeatable.
		for _, change := range pending.Changes {
			if err := apply(change.Entry, change.Before); err != nil {
				return fmt.Errorf("publication rollback failed; retain %s: %w", journalPath, err)
			}
		}
		if pending.Before == nil {
			if err := tsHandle.Remove(manifestName); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else if err := writeRootOutput(tsHandle, manifestName, pending.Before); err != nil {
			return err
		}
		if err := tsHandle.Remove(journalName); err != nil {
			return err
		}
	}
	if len(changes) == 0 && bytes.Equal(previous, nextBytes) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	journal, err := json.MarshalIndent(pendingPlan{Format: outputFormat, Before: previous, After: nextBytes, Changes: changes}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRootOutput(tsHandle, journalName, append(journal, '\n')); err != nil {
		return err
	}
	for _, change := range changes {
		if err := apply(change.Entry, change.After); err != nil {
			return fmt.Errorf("publication failed; retained recovery journal %s: %w", journalPath, err)
		}
	}
	if fail != nil {
		if err := fail("manifest", manifestPath); err != nil {
			return fmt.Errorf("publication failed; retained recovery journal %s: %w", journalPath, err)
		}
	}
	if err := writeRootOutput(tsHandle, manifestName, nextBytes); err != nil {
		return fmt.Errorf("publication manifest failed; retained %s: %w", journalPath, err)
	}
	if err := tsHandle.Remove(journalName); err != nil {
		return fmt.Errorf("publication journal cleanup failed: %w", err)
	}
	return nil
}
