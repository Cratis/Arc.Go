// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"fmt"
	"go/types"
	"reflect"
	"strings"

	"github.com/cratis/arc.go/internal/modelshape"
	"golang.org/x/tools/go/packages"
)

func compilerFields(t types.Type) ([]modelshape.WireField[types.Type], error) {
	return modelshape.Select(t, modelshape.Shape[types.Type]{
		Dereference: func(t types.Type) types.Type {
			t = types.Unalias(t)
			if pointer, ok := t.(*types.Pointer); ok {
				return types.Unalias(pointer.Elem())
			}
			return t
		},
		Members: func(t types.Type) ([]modelshape.Member[types.Type], bool) {
			st, ok := types.Unalias(t).Underlying().(*types.Struct)
			if !ok {
				return nil, false
			}
			members := make([]modelshape.Member[types.Type], st.NumFields())
			for i := range st.NumFields() {
				field := st.Field(i)
				members[i] = modelshape.Member[types.Type]{Name: field.Name(), Type: field.Type(), Tag: reflect.StructTag(st.Tag(i)), Exported: field.Exported(), Anonymous: field.Embedded()}
			}
			return members, true
		},
	})
}

type wireAnalyzer struct {
	graph               *Graph
	profile             ApplicationProfile
	packages            map[string]*packages.Package
	namespaces          map[string]string
	declarations        map[string]*model
	nodes               map[string]*TypeDescriptor
	compilerTypes       map[string]types.Type
	interfaceModels     map[string]*model
	typescript          bool
	typescriptRequested bool
	contract            bool
	sizes               types.Sizes
	seenTypes           map[string]bool
	tsRoots             []WireType
}

func (field FieldDescriptor) TagRules() bool {
	for _, rule := range field.Rules {
		if rule.Source == "explicit" {
			return true
		}
	}
	return false
}

func (w *wireAnalyzer) fail(t types.Type, format string, args ...any) error {
	if named, ok := types.Unalias(t).(*types.Named); ok {
		if pkg := w.packages[named.Obj().Pkg().Path()]; pkg != nil && pkg.Fset != nil {
			return diagnostic(pkg, named.Obj().Pos(), format, args...)
		}
	}
	return fmt.Errorf("wire type %s: %s", typeKey(t), fmt.Sprintf(format, args...))
}

func (w *wireAnalyzer) reference(pkg *types.Package, reference string) (types.Type, error) {
	if t := w.compilerTypes[reference]; t != nil {
		return t, nil
	}
	if pkg != nil {
		if t := lookupPackageReference(pkg, reference); t != nil {
			return t, nil
		}
	}
	if t := w.lookupImportedReference(reference); t != nil {
		return t, nil
	}
	return nil, fmt.Errorf("unknown wire type reference %q", reference)
}

// lookupPackageReference resolves a bare name in the package scope or an
// alias-qualified name through one of its imports.
func lookupPackageReference(pkg *types.Package, reference string) types.Type {
	if object := pkg.Scope().Lookup(reference); object != nil {
		return object.Type()
	}
	alias, member, ok := strings.Cut(reference, ".")
	if !ok {
		return nil
	}
	imported, ok := pkg.Scope().Lookup(alias).(*types.PkgName)
	if !ok {
		return nil
	}
	if object := imported.Imported().Scope().Lookup(member); object != nil {
		return object.Type()
	}
	return nil
}

// lookupImportedReference resolves a full "package/path.Name" reference
// against every indexed package.
func (w *wireAnalyzer) lookupImportedReference(reference string) types.Type {
	for path, imported := range w.packages {
		if !strings.HasPrefix(reference, path+".") || imported.Types == nil {
			continue
		}
		if object := imported.Types.Scope().Lookup(strings.TrimPrefix(reference, path+".")); object != nil {
			return object.Type()
		}
	}
	return nil
}

// constructorCycleWalker detects eager constructor reference cycles through
// base types and nested model fields.
type constructorCycleWalker struct {
	byKey map[string]TypeDescriptor
	state map[string]int
}

func rejectConstructorCycles(nodes []TypeDescriptor) error {
	walker := &constructorCycleWalker{byKey: map[string]TypeDescriptor{}, state: map[string]int{}}
	for _, node := range nodes {
		walker.byKey[node.Key] = node
	}
	for _, node := range nodes {
		if err := walker.walk(node.Key, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *constructorCycleWalker) walk(key string, chain []string) error {
	if c.state[key] == 2 {
		return nil
	}
	if c.state[key] == 1 {
		return fmt.Errorf("eager constructor reference cycle: %s", strings.Join(append(chain, key), " -> "))
	}
	c.state[key] = 1
	node := c.byKey[key]
	if node.Base != "" {
		if err := c.walk(node.Base, append(chain, key)); err != nil {
			return err
		}
	}
	if err := c.walkFields(node.Fields, append(chain, key)); err != nil {
		return err
	}
	c.state[key] = 2
	return nil
}

func (c *constructorCycleWalker) walkFields(fields []FieldDescriptor, chain []string) error {
	for _, field := range fields {
		wire := field.Type
		for wire.Element != nil {
			wire = *wire.Element
		}
		if wire.Kind != "model" {
			continue
		}
		if err := c.walk(wire.Target, chain); err != nil {
			return err
		}
	}
	return nil
}
