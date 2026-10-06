// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnumGeneratedConsumerAndStableTypeScript(t *testing.T) {
	dir := consumer(t)
	source := string(get(t, "testdata/enum/consumer.go"))
	put(t, filepath.Join(dir, "input.go"), source)
	config := Config{Dir: dir, TypeScriptOut: "web"}
	generate(t, config)
	adapter := get(t, filepath.Join(dir, Filename))
	if !bytes.Contains(adapter, []byte(".NewInt32Enum(map[string]State")) || !bytes.Contains(adapter, []byte(`"Read":`)) || bytes.Contains(adapter, []byte(`"Reader":`)) {
		t.Fatalf("generated parser did not preserve original declarations:\n%s", adapter)
	}
	first := get(t, filepath.Join(dir, "web", "EnumContract", "State.ts"))
	generate(t, config)
	if !bytes.Equal(adapter, get(t, filepath.Join(dir, Filename))) {
		t.Fatal("enum generation is nondeterministic")
	}
	generate(t, Config{Dir: dir, TypeScriptOut: "web", Check: true})
	put(t, filepath.Join(dir, "input.go"), strings.Replace(source, " parse=int32", "", 1))
	generate(t, config)
	if !bytes.Equal(first, get(t, filepath.Join(dir, "web", "EnumContract", "State.ts"))) {
		t.Fatal("parser opt-in changed TypeScript output")
	}
	put(t, filepath.Join(dir, "input.go"), source)
	generate(t, config)
	put(t, filepath.Join(dir, "consumer_test.go"), string(get(t, "testdata/enum/consumer_test.go")))
	put(t, filepath.Join(dir, "profile.json"), string(get(t, "../../../ContractTests/EnumContract/profile.json")))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("enum consumer: %v\n%s", err, output)
	}
}

func TestEnumGeneratorRejectsInvalidDeclarationsBeforePublication(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"width", "//arc:enum parse=int32\ntype State int64\nconst Read State = 1", "requires an int32 underlying type"},
		{"empty", "//arc:enum parse=int32\ntype State int32", "requires declared parse names"},
		{"case_collision", "//arc:enum parse=int32\ntype State int32\nconst (Read State = 1; READ State = 2)", "unique ignoring case"},
		{"non_ascii", "//arc:enum parse=int32\ntype State int32\nconst Réad State = 1", "ASCII identifiers"},
		{"handwritten", "//arc:enum parse=int32\ntype State int32\nconst Read State = 1\nfunc (*State) UnmarshalJSON([]byte) error { panic(\"executed\") }", "owns UnmarshalJSON"},
		{"custom_output", "//arc:enum parse=int32\ntype State int32\nconst Read State = 1\nfunc (State) MarshalJSON() ([]byte, error) { panic(\"executed\") }", "custom codec"},
		{"unsupported_parser", "//arc:enum parse=int64\ntype State int64", "enum parse must be int32"},
		{"unsupported_codec", "//arc:codec kind=geospatial\ntype Point struct{}", "arc:codec is not implemented"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "input.go"), "package consumer\n"+tc.source+"\n")
			err := Generate(t.Context(), Config{Dir: dir})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, Filename)); !os.IsNotExist(err) {
				t.Fatalf("failure published output: %v", err)
			}
		})
	}
}
