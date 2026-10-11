// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"go/types"
	"path/filepath"
	"reflect"

	"github.com/cratis/arc.go/metadata"
)

// packageNamespace resolves the namespace for a package, preferring the
// profile's explicit mapping over the one declared in source.
func (w *wireAnalyzer) packageNamespace(pkgPath string) (string, bool) {
	if configured, ok := w.profile.PackageNamespaces[pkgPath]; ok {
		return configured, true
	}
	namespace, exists := w.namespaces[pkgPath]
	return namespace, exists
}

func (w *wireAnalyzer) describeStructType(t types.Type, named *types.Named, key string) (WireType, bool, error) {
	if _, ok := named.Underlying().(*types.Struct); !ok {
		return WireType{}, false, nil
	}
	model := WireType{Kind: "model", Target: key}
	if w.nodes[key] != nil {
		return model, true, nil
	}
	if err := w.declareStructModel(t, named, key); err != nil {
		return WireType{}, true, err
	}
	return model, true, nil
}

func (w *wireAnalyzer) declareStructModel(t types.Type, named *types.Named, key string) error {
	namespace, exists := w.packageNamespace(named.Obj().Pkg().Path())
	if !exists {
		return w.fail(t, "reachable external wire models require an explicit package namespace or import mapping")
	}
	declaration := w.declarations[key]
	name := named.Obj().Name()
	if declaration != nil {
		name = declaration.d.name
	}
	node := &TypeDescriptor{Key: key, Name: metadata.TypeName{Namespace: namespace, Name: name}, Kind: "model", Source: w.sourceFile(named)}
	w.nodes[key] = node
	w.compilerTypes[key] = t
	fields, err := w.fields(t, false)
	if err != nil {
		return err
	}
	node.Fields = fields
	if declaration == nil || declaration.d.targetInterface == "" {
		return nil
	}
	return w.bindDerivative(t, named, node, declaration)
}

func (w *wireAnalyzer) sourceFile(named *types.Named) string {
	pkg := w.packages[named.Obj().Pkg().Path()]
	if pkg == nil {
		return ""
	}
	return filepath.Base(pkg.Fset.Position(named.Obj().Pos()).Filename)
}

func implementsInterface(named *types.Named, iface types.Type) bool {
	return types.AssignableTo(named, iface) || types.AssignableTo(types.NewPointer(named), iface)
}

func isNonemptyNamedInterface(t types.Type) bool {
	base, ok := types.Unalias(t).(*types.Named)
	return ok && base.Underlying().(*types.Interface).NumMethods() != 0
}

func (w *wireAnalyzer) validateDerivativeInterface(t types.Type, named *types.Named, baseInterface types.Type) error {
	if _, ok := baseInterface.Underlying().(*types.Interface); !ok || !implementsInterface(named, baseInterface) {
		return w.fail(t, "declared derivative must implement its interface")
	}
	if w.contract && !isNonemptyNamedInterface(baseInterface) {
		return w.fail(t, "declared derivative requires a named nonempty interface")
	}
	return nil
}

func (w *wireAnalyzer) bindDerivative(t types.Type, named *types.Named, node *TypeDescriptor, declaration *model) error {
	baseInterface, err := w.reference(named.Obj().Pkg(), declaration.d.targetInterface)
	if err != nil {
		return err
	}
	if err := w.validateDerivativeInterface(t, named, baseInterface); err != nil {
		return err
	}
	w.compilerTypes[typeKey(baseInterface)] = baseInterface
	node.Interface = typeKey(baseInterface)
	node.DerivedID = declaration.d.derivedID
	if w.contract {
		node.Discriminator = &DerivedContract{Property: "_derivedTypeId", Default: node.DerivedID == "", ID: node.DerivedID, OutputRequired: node.DerivedID != ""}
	}
	if declaration.d.derivedBase == "" {
		return nil
	}
	return w.bindDerivedBase(t, named, node, declaration.d.derivedBase)
}

func hasWireField(fields []FieldDescriptor, wanted FieldDescriptor) bool {
	for _, field := range fields {
		if field.Name == wanted.Name && reflect.DeepEqual(field.Type, wanted.Type) {
			return true
		}
	}
	return false
}

func (w *wireAnalyzer) bindDerivedBase(t types.Type, named *types.Named, node *TypeDescriptor, derivedBase string) error {
	base, err := w.reference(named.Obj().Pkg(), derivedBase)
	if err != nil {
		return err
	}
	wire, err := w.describe(base)
	if err != nil {
		return err
	}
	baseNode := w.nodes[wire.Target]
	if baseNode == nil || baseNode.Interface != node.Interface {
		return w.fail(t, "derived model requires a matching declared base contract")
	}
	for _, inherited := range baseNode.Fields {
		if !hasWireField(node.Fields, inherited) {
			return w.fail(t, "derived model does not preserve base wire field %s", inherited.Name)
		}
	}
	node.Base = wire.Target
	return nil
}
