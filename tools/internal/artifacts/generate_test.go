// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func get(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func consumer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// A real independently consumable module, with the tools module's fetchable
	// runtime pin and checksums. No workspace or replace directive is involved.
	manifest := string(get(t, "../../go.mod"))
	manifest = strings.Replace(manifest, "module github.com/cratis/arc.go/tools", "module example.test/consumer", 1)
	put(t, filepath.Join(dir, "go.mod"), manifest)
	put(t, filepath.Join(dir, "go.sum"), string(get(t, "../../go.sum")))
	return dir
}
func generate(t *testing.T, config Config) {
	t.Helper()
	if err := Generate(t.Context(), config); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapRegenerationAndExecutableConsumer(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "artifacts.go"), string(get(t, "testdata/consumer.go")))
	generate(t, Config{Dir: dir})
	path := filepath.Join(dir, Filename)
	first := get(t, path)
	generate(t, Config{Dir: dir})
	if !bytes.Equal(first, get(t, path)) {
		t.Fatal("regeneration is not byte deterministic")
	}
	generate(t, Config{Dir: dir, Check: true})
	// Old imports and invalid syntax cannot poison bootstrap.
	put(t, path, Header+"package stale\nimport \"example.invalid/deleted\"\nnot valid Go !!!")
	generate(t, Config{Dir: dir})
	if !bytes.Equal(first, get(t, path)) {
		t.Fatal("stale output changed regenerated content")
	}
	put(t, filepath.Join(dir, "consumer_test.go"), string(get(t, "testdata/consumer_test.go")))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated consumer did not compile/run: %v\n%s", err, output)
	}
}

func TestStaleOutputCleanupIsOwnedAndScoped(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "artifacts.go"), "package consumer\n")
	ownedPath := filepath.Join(dir, Filename)
	put(t, ownedPath, Header+"package consumer\nfunc Obsolete() {}\n")
	handwritten := filepath.Join(dir, "handwritten.go")
	put(t, handwritten, "package consumer\nconst Keep = true\n")
	outside := filepath.Join(dir, "unselected", Filename)
	put(t, outside, Header+"package unselected\n")
	if err := Generate(t.Context(), Config{Dir: dir, Check: true}); err == nil {
		t.Fatal("check accepted stale output")
	}
	generate(t, Config{Dir: dir})
	if _, err := os.Stat(ownedPath); !os.IsNotExist(err) {
		t.Fatal("owned stale file was not removed", err)
	}
	if !bytes.Contains(get(t, handwritten), []byte("Keep")) || len(get(t, outside)) == 0 {
		t.Fatal("changed unowned/unselected file")
	}
	put(t, ownedPath, "package consumer\nconst Handwritten = true\n")
	if err := Generate(t.Context(), Config{Dir: dir}); err == nil || !strings.Contains(err.Error(), "unowned") {
		t.Fatal("overwrote unowned output", err)
	}
	if !bytes.Contains(get(t, ownedPath), []byte("Handwritten")) {
		t.Fatal("unowned content changed")
	}
}

func TestBuildTagsAndDeletedMethods(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "base.go"), "package consumer\nfunc init() { panic(\"generator must not execute user code\") }\n")
	put(t, filepath.Join(dir, "tagged.go"), "//go:build selected\n\npackage consumer\n//arc:command\ntype Tagged struct{}\nfunc (Tagged) Handle() error { return nil }\n")
	generate(t, Config{Dir: dir, Tags: "selected"})
	path := filepath.Join(dir, Filename)
	old := get(t, path)
	if !bytes.Contains(old, []byte("Register[Tagged]")) {
		t.Fatal("tagged artifact absent")
	}
	// Build configuration no longer selects the declaration, so remove the adapter.
	generate(t, Config{Dir: dir})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("inactive artifact retained", err)
	}
	put(t, filepath.Join(dir, "base.go"), "package consumer\n//arc:command\ntype Missing struct{}\n")
	put(t, path, string(old))
	err := Generate(t.Context(), Config{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "declared Handle") || !regexp.MustCompile(`base.go:\d+:\d+`).MatchString(err.Error()) {
		t.Fatal("missing positional deleted-method diagnostic", err)
	}
	if !bytes.Equal(old, get(t, path)) {
		t.Fatal("failed analysis changed existing output")
	}
}

func TestDiagnostics(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"unknown", "//arc:commmand\ntype C struct{}", "unknown arc:commmand"},
		{"misspelled_security", "//arc:command\n//arc:authorize roels=Admin\ntype C struct{}\nfunc (C) Handle() error{return nil}", "unsupported option"},
		{"schemes", "//arc:command\n//arc:authorize schemes=Bearer\ntype C struct{}\nfunc (C) Handle() error{return nil}", "unsupported option"},
		{"anonymous_conflict", "//arc:command\n//arc:authorize\n//arc:allow-anonymous\ntype C struct{}", "conflict"},
		{"duplicate_option", "//arc:command name=A name=B\ntype C struct{}", "duplicate option"},
		{"empty_role", "//arc:command\n//arc:authorize roles=Admin,\ntype C struct{}", "invalid model metadata"},
		{"misplaced", "var x = 1 //arc:authorize\n", "misplaced arc directive"},
		{"variadic", "//arc:command\ntype C struct{}\nfunc (C) Handle(...int)error{return nil}", "variadic"},
		{"generic", "//arc:command\ntype C[T any] struct{}\nfunc(C[T])Handle()error{return nil}", "nongeneric struct"},
		{"alias", "type Original struct{}\n//arc:command\ntype C = Original", "defined nongeneric"},
		{"no_handle", "//arc:command\ntype C struct{}", "declared Handle"},
		{"ignored_handle", "//arc:command\ntype C struct{}\n//arc:ignore\nfunc(C)Handle()error{return nil}", "declared Handle"},
		{"ignored_validate", "import(\"context\";\"github.com/cratis/arc.go/validation\")\n//arc:command\ntype C struct{}\nfunc(C)Handle()error{return nil}\n//arc:ignore\nfunc(C)Validate(context.Context)([]validation.Result,error){return nil,nil}", "cannot disable runtime model validation"},
		{"promoted_handle", "type Base struct{}\nfunc(Base)Handle()error{return nil}\n//arc:command\ntype C struct{Base}", "declared Handle"},
		{"multi_return", "//arc:command\ntype C struct{}\nfunc(C)Handle()(int,string,error){return 0,\"\",nil}", "unsupported results"},
		{"unused_provided", "//arc:command\ntype C struct{}\nfunc(C)Provide()(int,error){return 0,nil}\nfunc(C)Handle()error{return nil}", "exactly one"},
		{"ambiguous_provided", "//arc:command\ntype C struct{}\nfunc(C)Provide()(int,error){return 0,nil}\nfunc(C)Handle(int,int)error{return nil}", "found 2"},
		{"pointer_provide", "//arc:command\ntype C struct{}\nfunc(*C)Provide()(int,error){return 0,nil}\nfunc(C)Handle(int)error{return nil}", "pointer Provide"},
		{"bad_validate", "//arc:command\ntype C struct{}\nfunc(C)Handle()error{return nil}\nfunc(C)Validate()error{return nil}", "Validate must match"},
		{"pointer_validate", "import(\"context\";\"github.com/cratis/arc.go/validation\")\n//arc:command\ntype C struct{}\nfunc(C)Handle()error{return nil}\nfunc(*C)Validate(context.Context)([]validation.Result,error){return nil,nil}", "Validate must match"},
		{"tag_conflict", "//arc:command\ntype C struct{_ struct{} `json:\"-\" arc:\"command\"`}", "tags and artifact directives"},
		{"named_query_receiver", "//arc:readmodel\ntype M struct{}\n//arc:query\nfunc(m M)All()(M,error){return m,nil}", "unnamed value receiver"},
		{"pointer_query_receiver", "//arc:readmodel\ntype M struct{}\n//arc:query\nfunc(*M)All()(M,error){return M{},nil}", "unnamed value receiver"},
		{"unowned_query", "//arc:query model=Missing\nfunc All()(int,error){return 0,nil}", "local arc:readmodel"},
		{"bad_query_result", "//arc:readmodel\ntype M struct{}\n//arc:query\nfunc(M)All()(chan M,error){return nil,nil}", "owning model"},
		{"bad_arguments", "//arc:readmodel\ntype M struct{}\nfunc(M)All(int)(M,error){return M{},nil}", "named argument struct"},
		{"duplicate_query", "//arc:readmodel\ntype M struct{}\nfunc(M)All()(M,error){return M{},nil}\n//arc:query model=M name=All\nfunc Other()(M,error){return M{},nil}", "duplicate query identity"},
		{"ignore_unknown", "//arc:ignore\n//arc:authroize\ntype C struct{}", "unknown arc:authroize"},
		{"name_collision", "//arc:command\ntype C struct{}\nfunc(C)Handle()error{return nil}\nfunc RegisterArtifacts(){}", "reserved for generated"},
	}
	dir := consumer(t)
	for _, tc := range cases {
		put(t, filepath.Join(dir, tc.name, "input.go"), "package example\n"+tc.source+"\n")
	}
	loaded, err := packages.Load(&packages.Config{Context: t.Context(), Dir: dir, Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports}, "./...")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*packages.Package{}
	for _, pkg := range loaded {
		byName[filepath.Base(pkg.PkgPath)] = pkg
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg := byName[tc.name]
			if pkg == nil {
				t.Fatal("package missing")
			}
			if len(pkg.Errors) > 0 {
				t.Fatal(pkg.Errors)
			}
			_, err := analyze(pkg)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !regexp.MustCompile(`input.go:\d+:\d+`).MatchString(err.Error()) {
				t.Fatalf("got %v, want positional %q", err, tc.want)
			}
		})
	}
}

func TestFailedPackageDoesNotPublishOtherOutputs(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "first", "input.go"), "package first\n//arc:command\ntype C struct{}\nfunc(C)Handle()error{return nil}\n")
	put(t, filepath.Join(dir, "second", "input.go"), "package second\n//arc:authorise\ntype C struct{}\n")
	if err := Generate(t.Context(), Config{Dir: dir, Patterns: []string{"./..."}}); err == nil {
		t.Fatal("accepted invalid security directive")
	}
	if _, err := os.Stat(filepath.Join(dir, "first", Filename)); !os.IsNotExist(err) {
		t.Fatal("published output before validating every package", err)
	}
}

func TestSymlinkOutputIsNeverFollowed(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), "package consumer\n")
	target := filepath.Join(t.TempDir(), "target.go")
	put(t, target, Header+"package consumer\n")
	if err := os.Symlink(target, filepath.Join(dir, Filename)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := Generate(t.Context(), Config{Dir: dir}); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatal("symlink output accepted", err)
	}
	if !bytes.Equal(get(t, target), []byte(Header+"package consumer\n")) {
		t.Fatal("symlink target changed")
	}
}

func FuzzDirectives(f *testing.F) {
	for _, seed := range []string{"command name=Add", "authorize roles=Editor,Admin policy=Write", "allow-anonymous", "query model=Item", "ignore", "namespace Shop.Inventory"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		file, err := parser.ParseFile(token.NewFileSet(), "input.go", "package input\n//arc:"+strings.ReplaceAll(text, "\n", " ")+"\ntype Input struct{}", parser.ParseComments)
		if err != nil {
			return
		}
		for _, group := range file.Comments {
			_, _ = parseDirectives(group)
		}
	})
}
