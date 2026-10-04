// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

const sourceRevision = "7c1e78075b737df64f69fddfaae83374f75e3612"

type provenance struct {
	Revision string            `json:"revision"`
	SDK      string            `json:"sdk"`
	Runtime  string            `json:"runtime"`
	DLL      string            `json:"dll"`
	Hashes   map[string]string `json:"hashes"`
}

func verifyProvenance(path, dll string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("ARC_HTTP_CONFORMANCE_PROVENANCE must identify the verified preparation output")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var p provenance
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.Revision != sourceRevision || p.SDK != "10.0.401" || p.Runtime != "10.0.12" || p.DLL != dll {
		return fmt.Errorf("incomplete or incompatible executable provenance")
	}
	if p.Hashes[dll] == "" {
		return fmt.Errorf("unverified C# executable")
	}
	// Reuse the preparation validator: Git-derived membership is the contract,
	// not a minimum file count or only the hashes the proof happens to list.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("cannot locate preparation validator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-B", filepath.Join(filepath.Dir(file), "verify.py"),
		"--check-provenance", path, "--dll", dll)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exact preparation membership: %w: %s", err, output)
	}
	return nil
}
