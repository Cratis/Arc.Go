// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"path/filepath"
	"testing"
)

// servicePublicationConsumer generates the identical successful bootstrap once
// in the parent test. Only immutable output bytes are reused: every negative
// case gets its own module, source files and subsequent production analysis.
func servicePublicationConsumer(t *testing.T) func(*testing.T) (string, Config) {
	t.Helper()
	const input = "package consumer\ntype Foo struct{}\nfunc NewFoo() Foo{return Foo{}}\n"
	const other = "package other\n//arc:command\ntype Add struct{}\nfunc (Add) Handle() error{return nil}\n"
	newConsumer := func(t *testing.T) (string, Config) {
		t.Helper()
		dir := consumer(t)
		put(t, filepath.Join(dir, "input.go"), input)
		put(t, filepath.Join(dir, "other", "input.go"), other)
		return dir, Config{Dir: dir, Patterns: []string{".", "./other"}, BindingsConfigFile: bindingsConfig(t, dir, serviceBindingsConfig{})}
	}
	dir, config := newConsumer(t)
	generate(t, config)
	rootOutput := string(get(t, filepath.Join(dir, Filename)))
	otherOutput := string(get(t, filepath.Join(dir, "other", Filename)))
	return func(t *testing.T) (string, Config) {
		t.Helper()
		dir, config := newConsumer(t)
		put(t, filepath.Join(dir, Filename), rootOutput)
		put(t, filepath.Join(dir, "other", Filename), otherOutput)
		return dir, config
	}
}
