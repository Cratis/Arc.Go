// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

func TestValidatorAndPolicyDirectivesMatchManualRegistrationInAnExternalConsumer(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "artifacts.go"), string(get(t, "testdata/consumer_registrations.go")))
	generate(t, Config{Dir: dir})
	path := filepath.Join(dir, Filename)
	first := get(t, path)
	generate(t, Config{Dir: dir, Check: true})
	for _, want := range []string{
		"arcgenvalidation.Register[rename](arcBuilder.Validators(), renameValidator{})",
		"arcgenvalidation.RegisterConcept[code](arcBuilder.Validators(), &codeValidator{})",
		"arcgenvalidation.RegisterScoped[owner](arcBuilder.Validators(), func(",
		`arcBuilder.Policies().Register("Editors", editors{}, arcgenauthorization.PolicyOptions{EvaluatesAnonymous: false})`,
		`arcgenauthorization.RegisterPolicy[auditors](arcBuilder.Policies(), "Auditors", func(`,
		"arcgenauthorization.PolicyOptions{EvaluatesAnonymous: true}, arcKeys",
		"ResolveOwnerRules", "ResolveAuditLog",
	} {
		if !bytes.Contains(first, []byte(want)) {
			t.Fatalf("generated adapter lacks %q:\n%s", want, first)
		}
	}
	put(t, filepath.Join(dir, "consumer_test.go"), string(get(t, "testdata/consumer_registrations_test.go")))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated validator/policy consumer did not compile or match manual registration: %v\n%s", err, output)
	}
	generate(t, Config{Dir: dir})
	if !bytes.Equal(first, get(t, path)) {
		t.Fatal("regeneration is not byte deterministic")
	}
}

// registrationDiagnosticCases are rejected declarations; each runs in its own
// package of one module so a single load covers every case.
var registrationDiagnosticCases = map[string]struct{ source, want string }{
	"missingvalidate": {`//arc:validator
type v struct{}`, "v must implement Validate(context.Context, T) ([]validation.Result, error)"},
	"modelbound": {`//arc:validator
type v struct{}
func (v) Validate(context.Context) ([]validation.Result, error) { return nil, nil }`, "v must implement Validate("},
	"lookalikeresult": {`//arc:validator
type v struct{}
func (v) Validate(context.Context, cmd) ([]fake.Result, error) { return nil, nil }`, "v must implement Validate("},
	"lookalikecontext": {`//arc:validator
type v struct{}
func (v) Validate(fake.Context, cmd) ([]validation.Result, error) { return nil, nil }`, "v must implement Validate("},
	"variadic": {`//arc:validator
type v struct{}
func (v) Validate(context.Context, ...cmd) ([]validation.Result, error) { return nil, nil }`, "v must implement Validate("},
	"errorresult": {`//arc:validator
type v struct{}
func (v) Validate(context.Context, cmd) error { return nil }`, "v must implement Validate("},
	"generic": {`//arc:validator
type v[T any] struct{}
func (v[T]) Validate(context.Context, T) ([]validation.Result, error) { return nil, nil }`, "arc:validator requires a defined nongeneric struct type"},
	"nonstruct": {`//arc:validator
type v func()
func (v) Validate(context.Context, cmd) ([]validation.Result, error) { return nil, nil }`, "arc:validator requires a struct type"},
	"method": {`type v struct{}
//arc:validator
func (v) Validate(context.Context, cmd) ([]validation.Result, error) { return nil, nil }`, "not a method"},
	"constructorresults": {`type v struct{}
func (v) Validate(context.Context, cmd) ([]validation.Result, error) { return nil, nil }
//arc:validator
func newV() (v, int) { return v{}, 0 }`, "constructor must return T or (T, error)"},
	"constructorvariadic": {`type v struct{}
func (v) Validate(context.Context, cmd) ([]validation.Result, error) { return nil, nil }
//arc:validator
func newV(...int) v { return v{} }`, "variadic or generic arc:validator constructors are unsupported"},
	"constructornonvalidator": {`//arc:validator
func newV() (int, error) { return 0, nil }`, "newV result must implement Validate("},
	"interfacemodel": {`//arc:validator
type v struct{}
func (v) Validate(context.Context, any) ([]validation.Result, error) { return nil, nil }`, "must be a concrete type"},
	"unnameablemodel": {`//arc:validator
type v struct{ fake.Base }`, "is not nameable from package"},
	"duplicatevalidator": {`//arc:validator
type v struct{}
func (v) Validate(context.Context, cmd) ([]validation.Result, error) { return nil, nil }
//arc:validator
func newV() v { return v{} }`, "duplicate arc:validator for"},
	"conceptvalue": {`//arc:validator concept=yes
type v struct{}`, "concept must be true or false"},
	"combined": {`//arc:validator
//arc:allow-anonymous
type v struct{}`, "arc:validator cannot be combined with other directives"},
	"commandvalidator": {`//arc:command
//arc:validator
type v struct{}`, "conflicting artifact directives"},
	"validatoronly": {`//arc:validator
type v struct{}
func (v) Validate(context.Context, int) ([]validation.Result, error) { return nil, nil }`, "need an arc:command, arc:readmodel or derived model in the same package"},
	"policyname": {`//arc:policy
type p struct{}`, "arc:policy requires name=<policy name>"},
	"policyoption": {`//arc:policy name=P roles=a
type p struct{}`, `unsupported option "roles=a" on arc:policy`},
	"policyanonymous": {`//arc:policy name=P evaluates-anonymous=1
type p struct{}`, "evaluates-anonymous must be true or false"},
	"policylookalikedecision": {`//arc:policy name=P
type p struct{}
func (p) Authorize(context.Context, authorization.Context) (fake.Decision, error) { return fake.Decision{}, nil }`, "p must implement Authorize(context.Context, authorization.Context) (authorization.Decision, error)"},
	"policylookalikecontext": {`//arc:policy name=P
type p struct{}
func (p) Authorize(context.Context, fake.Context) (authorization.Decision, error) { return authorization.Allow(), nil }`, "p must implement Authorize("},
	"policyconstructor": {`//arc:policy name=P
func newP() cmd { return cmd{} }`, "newP result must implement Authorize("},
	"policyduplicate": {`//arc:policy name=P
type p struct{}
func (p) Authorize(context.Context, authorization.Context) (authorization.Decision, error) { return authorization.Allow(), nil }
//arc:policy name=P
func newP() p { return p{} }`, `duplicate arc:policy name "P"`},
}

func TestValidatorAndPolicyDirectiveDiagnostics(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "fake", "fake.go"), `package fake
import (
	"context"
	"github.com/cratis/arc.go/validation"
)
type Result struct{ Message string }
type Decision struct{}
type Context interface{ context.Context }
type hidden struct{}
type Base struct{}
func (Base) Validate(context.Context, hidden) ([]validation.Result, error) { return nil, nil }
`)
	names := make([]string, 0, len(registrationDiagnosticCases))
	for name, c := range registrationDiagnosticCases {
		names = append(names, name)
		header := `package ` + name + `
import (
	"context"
	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/validation"
	"example.test/consumer/fake"
)
var _ = context.Background
var _ = authorization.Allow
var _ validation.Result
var _ fake.Result
`
		if name != "validatoronly" {
			header += "//arc:command\n//arc:allow-anonymous\ntype cmd struct{}\nfunc (cmd) Handle() error { return nil }\n"
		}
		put(t, filepath.Join(dir, name, name+".go"), header+c.source+"\n")
	}
	sort.Strings(names)
	patterns := make([]string, len(names))
	for i, name := range names {
		patterns[i] = "./" + name
	}
	loaded, err := packages.Load(&packages.Config{Context: t.Context(), Dir: dir, Mode: packages.NeedName | packages.NeedFiles | packages.NeedModule | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps | packages.NeedTypesSizes}, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(names) {
		t.Fatalf("loaded %d packages, want %d", len(loaded), len(names))
	}
	for _, pkg := range loaded {
		t.Run(pkg.Name, func(t *testing.T) {
			if len(pkg.Errors) != 0 {
				t.Fatal(pkg.Errors)
			}
			want := registrationDiagnosticCases[pkg.Name].want
			_, err := analyze(pkg)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if !strings.Contains(err.Error(), pkg.Name+".go:") {
				t.Fatalf("diagnostic %q lacks a source position", err)
			}
		})
	}
}
