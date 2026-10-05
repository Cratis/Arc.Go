// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// withoutArtifactOuts validates everything except output locations, which may
// still be absolute flag values at that point.
func withoutArtifactOuts(profile ApplicationProfile) ApplicationProfile {
	if profile.OpenAPI != nil {
		openAPI := *profile.OpenAPI
		openAPI.Out = ""
		profile.OpenAPI = &openAPI
	}
	if profile.Screenplay != nil {
		profile.Screenplay = &ScreenplayProfile{}
	}
	return profile
}

// artifactPath resolves a configured artifact location against the module
// root. Absolute locations must name a file inside the module.
func artifactPath(moduleRoot, kind, out, suffix string) (string, error) {
	relative := out
	if filepath.IsAbs(out) {
		var err error
		relative, err = filepath.Rel(moduleRoot, out)
		if err != nil || !filepath.IsLocal(relative) {
			// Tolerate a symlinked spelling of the module root (such as /var
			// and /private/var) in the trusted module path only; the output
			// file itself is never resolved.
			resolvedRoot, rootErr := filepath.EvalSymlinks(moduleRoot)
			resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(out))
			if rootErr != nil || parentErr != nil {
				return "", fmt.Errorf("%s output %q must be inside the module", kind, out)
			}
			relative, err = filepath.Rel(resolvedRoot, filepath.Join(resolvedParent, filepath.Base(out)))
			if err != nil {
				return "", fmt.Errorf("%s output %q must be inside the module", kind, out)
			}
		}
	}
	relative = filepath.ToSlash(relative)
	if err := validateArtifactOut(kind, relative, suffix); err != nil {
		return "", err
	}
	return filepath.Join(moduleRoot, filepath.FromSlash(relative)), nil
}

// renderArtifacts renders the requested OpenAPI and Screenplay files from the
// finalized graph. Unsupported shapes refuse the whole artifact with the
// renderer's diagnostic; nothing is written by this function.
func renderArtifacts(moduleRoot string, profile ApplicationProfile, graph *Graph) ([]ownedOutput, error) {
	var outputs []ownedOutput
	if profile.OpenAPI != nil {
		path, err := artifactPath(moduleRoot, "openapi", profile.OpenAPI.Out, ".json")
		if err != nil {
			return nil, err
		}
		document, err := renderOpenAPI(graph)
		if err != nil {
			return nil, err
		}
		var indented bytes.Buffer
		// Indent preserves every numeric token; only insignificant space changes.
		if err := json.Indent(&indented, document.bytes(), "", "  "); err != nil {
			return nil, fmt.Errorf("openapi: format: %w", err)
		}
		indented.WriteByte('\n')
		outputs = append(outputs, ownedOutput{Path: path, Content: indented.Bytes()})
	}
	if profile.Screenplay != nil {
		path, err := artifactPath(moduleRoot, "screenplay", profile.Screenplay.Out, ".play")
		if err != nil {
			return nil, err
		}
		export, err := exportScreenplayMetadata(graph)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, ownedOutput{Path: path, Content: append([]byte(Header), export.Document...)})
	}
	return outputs, nil
}

// moduleManifestExists reports whether an artifact-only publication left a
// manifest (or an interrupted journal) in the module root. Any non-regular
// entry counts as existing so publication can refuse it explicitly.
func moduleManifestExists(moduleRoot string) bool {
	for _, name := range []string{manifestName, journalName} {
		if _, exists, err := readOutput(filepath.Join(moduleRoot, name)); exists || err != nil {
			return true
		}
	}
	return false
}

// moduleManifestMatches limits artifact-less reconciliation to the current
// owner and package/build scope. Pending publication uses its intended manifest,
// including interrupted first publication where no live manifest exists yet.
func moduleManifestMatches(moduleRoot string, next ownedManifest) (bool, error) {
	data, exists, err := readOutput(filepath.Join(moduleRoot, manifestName))
	if err != nil {
		return false, err
	}
	pendingBytes, pendingExists, err := readOutput(filepath.Join(moduleRoot, journalName))
	if err != nil {
		return false, err
	}
	if pendingExists {
		var pending pendingPlan
		if err := decodeOwned(pendingBytes, &pending); err != nil {
			return false, fmt.Errorf("invalid pending publication: %w", err)
		}
		if pending.Format != outputFormat || len(pending.After) == 0 {
			return false, fmt.Errorf("unsupported pending publication")
		}
		data, exists = pending.After, true
	}
	if !exists {
		return false, nil
	}
	var old ownedManifest
	if err := decodeOwned(data, &old); err != nil {
		return false, fmt.Errorf("invalid ownership manifest: %w", err)
	}
	if old.Format != outputFormat {
		return false, fmt.Errorf("unsupported ownership manifest")
	}
	return old.Owner == next.Owner && old.Scope == next.Scope, nil
}

func reportArtifacts(report io.Writer, outputs []ownedOutput, check bool) error {
	if report == nil || len(outputs) == 0 {
		return nil
	}
	paths := make([]string, len(outputs))
	for i, output := range outputs {
		paths[i] = output.Path
	}
	verb := "published"
	if check {
		verb = "verified"
	}
	_, err := fmt.Fprintf(report, "arc-gen: %s %s\n", strings.Join(paths, ", "), verb)
	return err
}
