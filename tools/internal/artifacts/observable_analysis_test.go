// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import (
	"bytes"
	"context"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObservableAnalysisSeparatesDeclarationEmissionAndData(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), string(get(t, "testdata/observable_consumer.go")))
	put(t, filepath.Join(dir, "shared", "source.go"), "package shared\nimport \"github.com/cratis/arc.go/observable\"\ntype Source[T any] = observable.Source[T]\n")
	put(t, filepath.Join(dir, "alias.go"), "package consumer\nimport \"example.test/consumer/shared\"\nfunc (Task) CrossPackageAlias() (shared.Source[[]Task],error) { return nil,nil }\n")
	analyses := graphPackages(t, dir, ".")
	graph, err := buildGraph(analyses, ApplicationProfile{FormatVersion: GraphVersion, Name: "observables"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Queries) != 13 {
		t.Fatalf("queries = %d", len(graph.Queries))
	}
	for _, q := range analyses[0].queries {
		d := q.descriptor
		if q.d.name == "All" {
			if q.emission != nil || d.Delivery != "snapshot" || d.Declaration.Observable {
				t.Fatal("slice became observable")
			}
			continue
		}
		if q.emission == nil || types.Identical(q.call.output, q.emission) || d.Delivery != "observable" || !d.Declaration.Observable {
			t.Fatalf("lost source declaration: %s", q.d.name)
		}
		want := "array"
		switch q.d.name {
		case "Single", "Nullable", "Current", "Subject", "Pending":
			want = "model"
		}
		if d.Result.Kind != want || d.Paged != (q.d.name == "Page") || d.Result.Nullable != (q.d.name == "Nullable") {
			t.Fatalf("wrong emission data: %+v", d)
		}
		if d.Declaration.ReadModelIdentityMember != "id" || len(d.SortFields) != 1 {
			t.Fatal("lost model metadata")
		}
	}
	for _, q := range graph.Catalog.Queries {
		if q.Observable != (q.Name != "All") {
			t.Fatal("catalog delivery drift")
		}
	}
	adapter, err := emit(analyses[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(adapter, []byte("RegisterObservable[Task, WatchArgs, []Task]")) || bytes.Contains(adapter, []byte("WithEnumerable")) || bytes.Contains(adapter, []byte(".Open(")) {
		t.Fatalf("wrong source adapter:\n%s", adapter)
	}
	generate(t, Config{Dir: dir})
	put(t, filepath.Join(dir, "contract_test.go"), string(get(t, "testdata/observable_consumer_test.go")))
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=mod", "-count=1", "-timeout=30s", "./...")
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("independent generated observable consumer: %v\n%s", err, output)
	}
}

func TestObservableInvalidEmissionsRemainQueryCandidates(t *testing.T) {
	for _, output := range []string{
		"observable.Source[string]", "observable.Source[Foreign]", "observable.Source[observable.Source[Task]]",
		"observable.Source[*queries.Page[Task]]", "observable.Source[*queries.ObservedCollection[Task]]",
		"observable.Source[NamedTasks]", "observable.Source[chan Task]", "*observable.Source[Task]", "observable.State[Task]",
	} {
		t.Run(output, func(t *testing.T) {
			dir := consumer(t)
			put(t, filepath.Join(dir, "input.go"), "package consumer\nimport (\"github.com/cratis/arc.go/observable\"; \"github.com/cratis/arc.go/queries\")\nvar _ queries.NoArguments\n//arc:readmodel\ntype Task struct{}\ntype Foreign struct{}\ntype NamedTasks []Task\nfunc (Task) Watch() ("+output+", error) { var zero "+output+"; return zero, nil }\n")
			err := Generate(t.Context(), Config{Dir: dir})
			if err == nil || !strings.Contains(err.Error(), "query") && !strings.Contains(err.Error(), "observable declaration") {
				t.Fatalf("invalid emission silently omitted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, Filename)); !os.IsNotExist(err) {
				t.Fatal("invalid emission wrote adapter")
			}
		})
	}
}

func TestObservablePointerElementsGoOnlyAndForeignSourcesRejected(t *testing.T) {
	dir := consumer(t)
	put(t, filepath.Join(dir, "input.go"), `package consumer
import "github.com/cratis/arc.go/observable"
//arc:readmodel
type Task struct{}
func (Task) Watch() (observable.Source[[]*Task], error) { return nil, nil }
`)
	generate(t, Config{Dir: dir})
	if !bytes.Contains(get(t, filepath.Join(dir, Filename)), []byte("RegisterObservable[Task, arcgenqueries.NoArguments, []*Task]")) {
		t.Fatal("Go pointer elements unsupported")
	}
	// A foreign structurally compatible source is not a declared supported source.
	put(t, filepath.Join(dir, "foreign.go"), `package consumer
import ("context"; "github.com/cratis/arc.go/observable")
type Opaque[T any] interface { Open(context.Context) (observable.Stream[T], error) }
//arc:query model=Task
func ForeignSource() (Opaque[Task], error) { return nil, nil }
`)
	if err := Generate(t.Context(), Config{Dir: dir}); err == nil {
		t.Fatal("foreign opaque source accepted")
	}
}
