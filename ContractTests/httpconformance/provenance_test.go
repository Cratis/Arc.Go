// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package httpconformance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExecutableProvenanceRejectsMissingChangedAndWrongPins(t *testing.T) {
	dir := t.TempDir()
	proof := provenance{Revision: sourceRevision, SDK: "10.0.401", Runtime: "10.0.12", Hashes: map[string]string{}}
	body := []byte("prepared fixture")
	hash := sha256.Sum256(body)
	for i := range 624 {
		path := filepath.Join(dir, fmt.Sprintf("source-%d", i))
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		proof.Hashes[path] = hex.EncodeToString(hash[:])
		if i == 0 {
			proof.DLL = path
		}
	}
	path := filepath.Join(dir, "proof.json")
	write := func() {
		t.Helper()
		data, err := json.Marshal(proof)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if verifyProvenance(path, proof.DLL) == nil {
		t.Fatal("arbitrary 624-file inventory admitted without required source membership")
	}
	proof.SDK = "10.0.400"
	write()
	if verifyProvenance(path, proof.DLL) == nil {
		t.Fatal("wrong SDK admitted")
	}
	proof.SDK = "10.0.401"
	write()
	if err := os.WriteFile(proof.DLL, []byte("changed fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyProvenance(path, proof.DLL) == nil {
		t.Fatal("changed executable admitted")
	}
	if verifyProvenance(filepath.Join(dir, "missing.json"), proof.DLL) == nil {
		t.Fatal("missing proof admitted")
	}
}

func TestSourceMembershipRegressions(t *testing.T) {
	command := exec.CommandContext(t.Context(), "python3", "-B", "-m", "unittest", "-v", "test_source_inventory")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("source membership regressions: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func TestEnvelopeRequiresCorrelationAndCompleteMembers(t *testing.T) {
	body := []byte(`{"correlationId":"00112233-4455-4677-8899-aabbccddeeff","paging":{"page":0,"size":0,"totalItems":0,"totalPages":0},"isSuccess":true,"isAuthorized":true,"isValid":true,"isReady":true,"hasExceptions":false,"exceptionMessages":[],"exceptionStackTrace":"","validationResults":[],"data":[]}`)
	header := make(http.Header)
	header.Set("X-Correlation-ID", correlationID)
	e := exchange{Status: 200, Header: header, Body: body}
	if err := checkEnvelope(e); err != nil {
		t.Fatal(err)
	}
	missing := e
	missing.Body = []byte(`{"correlationId":"00112233-4455-4677-8899-aabbccddeeff"}`)
	if checkEnvelope(missing) == nil {
		t.Fatal("incomplete envelope admitted")
	}
	wrong := e
	wrong.Header = make(http.Header)
	if checkEnvelope(wrong) == nil {
		t.Fatal("missing correlation echo admitted")
	}
}
