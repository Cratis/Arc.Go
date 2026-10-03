// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if p.Revision != sourceRevision || p.SDK != "10.0.401" || p.Runtime != "10.0.12" || p.DLL != dll || len(p.Hashes) < 624 {
		return fmt.Errorf("incomplete or incompatible executable provenance")
	}
	if p.Hashes[dll] == "" {
		return fmt.Errorf("unverified C# executable")
	}
	for path, want := range p.Hashes {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("relative provenance path %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != want {
			return fmt.Errorf("prepared input changed: %s", path)
		}
	}
	return nil
}
