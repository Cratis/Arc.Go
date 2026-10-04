// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	di "github.com/cratis/fundamentals.go/dependencyinjection"
	bt "github.com/cratis/fundamentals.go/dependencyinjection/bindingtypes"
	"golang.org/x/tools/go/packages"
)

func serviceAnalysis(t *testing.T, source string) *analysis {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "services.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("example.test/consumer", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	return &analysis{pkg: &packages.Package{PkgPath: pkg.Path(), Name: pkg.Name(), Types: pkg, TypesInfo: info, Fset: fset, Syntax: []*ast.File{file}}}
}
func TestServicePlannerSelectionAndExactKeys(t *testing.T) {
	a := serviceAnalysis(t, `package consumer
 import "context"
 //cratis:scoped
 type Foo struct{}
 func NewFoo(context.Context) (*Foo,error) { return nil,nil }
 func (*Foo) Name() {}
 type IFoo interface { Name() }
 type Alias = Foo
 type Consumer struct{}
 func NewConsumer(a,b *Alias) Consumer { return Consumer{} }
 //cratis:ignore-convention
 type Ignore struct{}
 func NewIgnore() Ignore { return Ignore{} }
 `)
	var report bytes.Buffer
	plan, err := planServiceBindings([]*analysis{a}, &serviceBindingsConfig{Package: a.pkg.PkgPath, MatchIFoo: true, RequireAllDependencies: true}, &report)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.plan.Bindings) != 3 {
		t.Fatal(plan.plan)
	}
	var constructor, forward, consumer bt.Binding
	for _, b := range plan.plan.Bindings {
		switch {
		case b.Constructor != nil && b.Constructor.Name() == "NewFoo":
			constructor = b
		case b.Forward != nil:
			forward = b
		case b.Constructor != nil && b.Constructor.Name() == "NewConsumer":
			consumer = b
		}
	}
	if !constructor.PassContext || !constructor.ReturnsError || constructor.Lifetime != di.Scoped || forward.Lifetime != di.Scoped || forward.Ownership != di.Borrowed || !types.Identical(forward.Forward, constructor.Service) {
		t.Fatal(plan.plan)
	}
	if len(consumer.Arguments) != 2 || len(consumer.Dependencies) != 1 || !types.Identical(consumer.Arguments[0], constructor.Service) {
		t.Fatal(consumer)
	}
	empty, err := planServiceBindings([]*analysis{a}, &serviceBindingsConfig{Package: a.pkg.PkgPath, Constructors: []string{}}, nil)
	if err != nil || len(empty.plan.Bindings) != 0 {
		t.Fatal("explicit empty discovers constructors", err)
	}
	optout, err := planServiceBindings([]*analysis{a}, &serviceBindingsConfig{Package: a.pkg.PkgPath}, nil)
	if err != nil || len(optout.plan.Bindings) != 2 {
		t.Fatal("IFoo matching enabled by default", err)
	}
}
func TestServicePlannerFailuresAndObligations(t *testing.T) {
	cases := []struct {
		name, source string
		cfg          serviceBindingsConfig
		want         string
	}{
		{name: "directive", source: "//cratis:singleton\nfunc Bad() {}", want: "BT002 error"},
		{name: "conflicting policy", source: "//cratis:singleton\n//cratis:scoped\ntype Foo struct{}", want: "BT003 error"},
		{name: "variadic", source: "type Foo struct{}; func NewFoo(...int) Foo { return Foo{} }", want: "BT006 error"},
		{name: "generic", source: "type Foo[T any] struct{}; func NewFoo[T any]() Foo[T] { return Foo[T]{} }", want: "BT006 error"},
		{name: "zero results", source: "type Foo struct{}; func NewFoo() {}", want: "BT006 error"},
		{name: "wrong error result", source: "type Foo struct{}; func NewFoo() (Foo,bool) { return Foo{},false }", want: "BT006 error"},
		{name: "nonleading context", source: "type Foo struct{}; func NewFoo(a int,c context.Context) Foo { return Foo{} }", want: "BT006 error"},
		{name: "scalar", source: "type Foo struct{}; func NewFoo(int) Foo { return Foo{} }", want: "BT012 error"},
		{name: "strict missing", source: "type Foo struct{}; type Dep struct{}; func NewFoo(*Dep) Foo { return Foo{} }", cfg: serviceBindingsConfig{RequireAllDependencies: true}, want: "BT011 error"},
		{name: "missing external obligation", source: "type Foo struct{}; type Dep struct{}; func NewFoo(*Dep) Foo { return Foo{} }", want: "BT011 information"},
		{name: "ambiguous constructors", source: "type Foo struct{}; func A() Foo { return Foo{} }; func B() Foo { return Foo{} }", cfg: serviceBindingsConfig{Constructors: []string{"A", "B"}}, want: "BT005 error"},
		{name: "constructorless competitor", source: "type Foo struct{}; func NewFoo() *Foo { return nil }; func (*Foo) M(){}; type IFoo interface{ M() }; type Other struct{}; func (Other) M(){}", cfg: serviceBindingsConfig{MatchIFoo: true}, want: "BT009 error"},
		{name: "ignored competitor", source: "type Foo struct{}; func NewFoo() *Foo { return nil }; func (*Foo) M(){}; type IFoo interface{ M() };\n//cratis:ignore-convention\ntype Other struct{}; func (Other) M(){}", cfg: serviceBindingsConfig{MatchIFoo: true}, want: "BT009 error"},
		{name: "wrong exact pointer", source: "type Foo struct{}; func NewFoo() Foo { return Foo{} }; func (*Foo) M(){}; type IFoo interface{ M() }", cfg: serviceBindingsConfig{Interfaces: []serviceInterfaceConfig{{Service: "IFoo", Implementation: "Foo"}}}, want: "BT008 error"},
		{name: "missing exact key", source: "type Foo struct{}; func NewFoo() Foo { return Foo{} }; func (Foo) M(){}; type IFoo interface{ M() }", cfg: serviceBindingsConfig{Interfaces: []serviceInterfaceConfig{{Service: "IFoo", Implementation: "*Foo"}}}, want: "BT004 error"},
		{name: "unsafe wide disposable", source: "type Foo struct{}; func (Foo) Close() error{return nil}; type Dep struct{}; func NewFoo(a,b,c,d,e *Dep) Foo { return Foo{} }", want: "BT006 error"},
		{name: "missing lifetime", source: "type Foo struct{}; func NewFoo() Foo { return Foo{} }", cfg: serviceBindingsConfig{Existing: []serviceExistingConfig{{Key: "Foo"}}}, want: "actual lifetime attestation"},
		{name: "strict overlap", source: "type Foo struct{}; func NewFoo() Foo { return Foo{} }", cfg: serviceBindingsConfig{Existing: []serviceExistingConfig{{Key: "Foo", Lifetime: "singleton"}}}, want: "BT010 error"},
		{name: "duplicate alias existing", source: "type Foo struct{}; type Alias = Foo", cfg: serviceBindingsConfig{Existing: []serviceExistingConfig{{Key: "Foo", Lifetime: "singleton"}, {Key: "Alias", Lifetime: "singleton"}}}, want: "BT010 error"},
		{name: "symbol collision", source: "func RegisterServices() {}", want: "RegisterServices conflicts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := "package consumer\nimport \"context\"\nvar _ context.Context\n" + tc.source
			a := serviceAnalysis(t, source)
			cfg := tc.cfg
			cfg.Package = a.pkg.PkgPath
			var report bytes.Buffer
			_, err := planServiceBindings([]*analysis{a}, &cfg, &report)
			actual := report.String()
			if err != nil {
				actual += err.Error()
			}
			if !strings.Contains(actual, tc.want) {
				t.Fatalf("got %v / %s; want %s", err, report.String(), tc.want)
			}
			if strings.Contains(tc.want, "information") && err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestServicePlannerKeepExistingAttestsEffectiveForwardLifetime(t *testing.T) {
	a := serviceAnalysis(t, `package consumer
 type Foo struct{}
 func NewFoo() *Foo { return nil }
 func (*Foo) M(){}
 type IFoo interface { M() }
 `)
	var report bytes.Buffer
	plan, err := planServiceBindings([]*analysis{a}, &serviceBindingsConfig{Package: a.pkg.PkgPath, MatchIFoo: true, Duplicates: "keepExisting", Existing: []serviceExistingConfig{{Key: "*Foo", Lifetime: "singleton"}}}, &report)
	if err != nil || len(plan.plan.Bindings) != 2 || len(plan.existing) != 1 {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "BT013 information") {
		t.Fatal(report.String())
	}
	for _, b := range plan.plan.Bindings {
		if b.Lifetime != di.Singleton {
			t.Fatal(b)
		}
		if b.Forward == nil && b.Action != bt.RetainExisting {
			t.Fatal(b)
		}
	}
	data, err := emit(a, plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("NewFoo()")) || !bytes.Contains(data, []byte("BindBorrowed")) || !bytes.Contains(data, []byte("Contains(")) {
		t.Fatal(string(data))
	}
}
func TestServicePlannerForeignSignaturesMustBeAccessible(t *testing.T) {
	owner := serviceAnalysis(t, "package owner\n")
	// Check the foreign declaration with a distinct path in this compiler universe.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "foreign.go", "package foreign\ntype hidden struct{}\nfunc NewHidden() hidden{return hidden{}}\n", parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	pkg, err := new(types.Config).Check("example.test/foreign", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	foreign := &analysis{pkg: &packages.Package{PkgPath: pkg.Path(), Name: pkg.Name(), Types: pkg, TypesInfo: info, Fset: fset, Syntax: []*ast.File{file}}}
	_, err = planServiceBindings([]*analysis{owner, foreign}, &serviceBindingsConfig{Package: owner.pkg.PkgPath, Constructors: []string{"example.test/foreign.NewHidden"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "BT007 error") {
		t.Fatal("inaccessible foreign signature rendered", err)
	}
	_, err = planServiceBindings([]*analysis{owner}, &serviceBindingsConfig{Package: "not/selected"}, nil)
	if err == nil {
		t.Fatal("unselected service owner accepted")
	}
}

func TestServiceBindingsConfigStrictness(t *testing.T) {
	for _, input := range []string{
		`{"formatVersion":2,"package":"x"}`, `{"formatVersion":1}`, `{"formatVersion":1,"package":"x","unknown":1}`,
		`{"formatVersion":1,"package":"x","package":"y"}`, `{"formatVersion":1,"package":"x","constructors":null}`,
		`{"formatVersion":1,"package":"x","Constructors":null}`, `{"formatVersion":1,"package":"x","existing":[{"Key":"Foo","lifetime":"singleton"}]}`,
		`{"formatVersion":1,"package":"x","existing":[{"key":"Foo","lifetime":"singleton","lifetime":"scoped"}]}`,
		`{"formatVersion":1,"package":"x","duplicates":"skip"}`, `{"formatVersion":1,"package":"x"} {}`,
	} {
		t.Run(input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bindings.json")
			put(t, path, input)
			if _, err := readServiceBindingsConfig(path); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}
func TestServiceReferencesAreExactCompilerNames(t *testing.T) {
	a := serviceAnalysis(t, "package consumer\nimport \"context\"\nvar _ context.Context\ntype Alias = context.Context\ntype Foo struct{}\n")
	r := serviceReferences{owner: a.pkg.Types, packages: map[string]*types.Package{"context": a.pkg.Types.Imports()[0]}}
	for _, name := range []string{"Alias", "*Foo", "context.Context", "string"} {
		if _, err := r.typ(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"foo", "consumer.Foo", "**Foo", "context.missing", "Missing[Foo]", "not/imported.Type", "NewFoo()"} {
		if _, err := r.typ(name); err == nil {
			t.Fatalf("approximated %s", name)
		}
	}
}
