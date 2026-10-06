// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package mongodb

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var errBoundary = errors.New("private failure with sensitive document contents")

type recordingCollection struct {
	total                   int64
	cursor                  *recordingCursor
	countErr, findErr       error
	countFilter, findFilter bson.Raw
	window                  snapshotWindow
	counts, finds           int
	afterCount              func(context.Context)
}

func (f *recordingCollection) count(ctx context.Context, filter bson.Raw) (int64, error) {
	f.counts++
	f.countFilter = bytes.Clone(filter)
	if f.afterCount != nil {
		f.afterCount(ctx)
	}
	return f.total, f.countErr
}
func (f *recordingCollection) find(_ context.Context, filter bson.Raw, window snapshotWindow) (snapshotCursor, error) {
	f.finds++
	f.findFilter, f.window = bytes.Clone(filter), window
	if f.cursor == nil {
		return nil, f.findErr
	}
	return f.cursor, f.findErr
}

type recordingCursor struct {
	documents              []bson.Raw
	index                  int
	buffer                 bson.Raw
	iterationErr, closeErr error
	closes                 int
	beforeNext             func(context.Context, int)
	closeCall              func(context.Context)
	panicNext, panicClose  bool
}

func (c *recordingCursor) next(ctx context.Context) bool {
	if c.panicNext {
		panic("secret cursor panic")
	}
	if c.beforeNext != nil {
		c.beforeNext(ctx, c.index)
	}
	if ctx.Err() != nil || c.index == len(c.documents) {
		return false
	}
	// Reuse exactly the same storage like a driver batch buffer.
	c.buffer = append(c.buffer[:0], c.documents[c.index]...)
	c.index++
	return true
}
func (c *recordingCursor) current() bson.Raw { return c.buffer }
func (c *recordingCursor) err() error        { return c.iterationErr }
func (c *recordingCursor) close(ctx context.Context) error {
	c.closes++
	if c.closeCall != nil {
		c.closeCall(ctx)
	}
	if c.panicClose {
		panic("secret cleanup panic")
	}
	return c.closeErr
}
func rawDocument(t *testing.T, document bson.D) bson.Raw {
	t.Helper()
	data, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func rawAuthor(t *testing.T, id int32, name string) bson.Raw {
	t.Helper()
	return rawDocument(t, bson.D{{Key: "_id", Value: id}, {Key: "Name", Value: name}})
}
func fakeRenderer(t *testing.T, ownership Ownership, config RendererOptions[author], fake *recordingCollection) *Renderer[author] {
	t.Helper()
	binding, err := NewCollection[author](&mongo.Client{}, CollectionOptions{Database: "Library", Name: "Authors", Ownership: ownership, SortFields: []queries.SortField{"id", "name"}})
	if err != nil {
		t.Fatal(err)
	}
	if config.RowFilter == nil {
		config.RowFilter = func(context.Context, queries.QueryContext) (bson.D, error) { return bson.D{}, nil }
	}
	renderer, err := NewRenderer(binding, config)
	if err != nil {
		t.Fatal(err)
	}
	renderer.open = func(tenancy.ID) (snapshotCollection, error) { return fake, nil }
	return renderer
}
func snapshotPipeline(t *testing.T, renderer *Renderer[author], filter bson.D, configure func(*queries.Registry)) queries.Pipeline {
	t.Helper()
	var registry queries.Registry
	err := queries.RegisterRenderer[Find[author], []author](&registry, func(context.Context, *execution.Scope) (queries.Renderer[Find[author], []author], error) {
		return renderer, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = queries.Register[author](&registry, "Snapshot", queries.Function(func(context.Context, queries.NoArguments) (Find[author], error) {
		return Find[author]{Filter: filter}, nil
	}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true}))
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(&registry)
	}
	pipeline, err := registry.Build(queries.PipelineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return pipeline
}
func runSnapshot(t *testing.T, ctx context.Context, renderer *Renderer[author], filter bson.D, p queries.Parameters) (queries.Result[[]author], error) {
	t.Helper()
	return queries.Perform[[]author](ctx, snapshotPipeline(t, renderer, filter, nil), "author.Snapshot", queries.RequestFor(queries.NoArguments{}, p))
}
func noPublication(t *testing.T, result queries.Result[[]author], err error) {
	t.Helper()
	if err == nil && result.IsSuccess() {
		t.Fatal("expected failure")
	}
	if data, present := result.Data(); present || data != nil || result.Details().Paging.TotalItems != 0 {
		t.Fatalf("partial publication: %+v", result.Details())
	}
}

func TestRendererConstructorIsInertAndCopiesOptions(t *testing.T) {
	binding, err := NewCollection[author](&mongo.Client{}, CollectionOptions{Database: "Library", Name: "Authors", Ownership: ApplicationOwned})
	if err != nil {
		t.Fatal(err)
	}
	row := func(context.Context, queries.QueryContext) (bson.D, error) {
		t.Fatal("constructor activated callback")
		return nil, nil
	}
	config := RendererOptions[author]{RowFilter: row}
	r, err := NewRenderer(binding, config)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxItems = 1
	if r.options.MaxItems != 1000 || r.options.MaxBSONBytes != 16<<20 || r.options.Timeout != 10*time.Second || r.collection.client != binding.client {
		t.Fatal("defaults/ownership/config snapshot")
	}
	for _, invalid := range []RendererOptions[author]{{}, {RowFilter: row, MaxItems: -1}, {RowFilter: row, MaxItems: math.MaxInt32}, {RowFilter: row, MaxBSONBytes: -1}, {RowFilter: row, Timeout: -1}, {RowFilter: row, Release: func(context.Context, []bson.Raw) ([]author, error) { return nil, nil }}} {
		if _, err := NewRenderer(binding, invalid); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("error %v", err)
		}
	}
	binding.options.Ownership = ChronicleOwned
	if _, err := NewRenderer(binding, RendererOptions[author]{RowFilter: row}); !errors.Is(err, ErrReleaseRequired) {
		t.Fatal(err)
	}
	if _, err := NewRenderer[author](nil, RendererOptions[author]{RowFilter: row}); !errors.Is(err, ErrConfiguration) {
		t.Fatal(err)
	}
	if _, exists := reflect.TypeOf(r).MethodByName("Close"); exists {
		t.Fatal("renderer owns client")
	}
}

func TestAuthorizedFrozenFilterServerWindowAndRawCopies(t *testing.T) {
	row := bson.D{{Key: "Owner", Value: "allowed"}}
	query := bson.D{{Key: "Owner", Value: "denied"}, {Key: "$or", Value: bson.A{bson.D{}}}}
	fake := &recordingCollection{total: 7, cursor: &recordingCursor{documents: []bson.Raw{rawAuthor(t, 2, "Ada"), rawAuthor(t, 3, "Eve")}}}
	calls := 0
	r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{RowFilter: func(context.Context, queries.QueryContext) (bson.D, error) { calls++; return row, nil }}, fake)
	fake.afterCount = func(context.Context) { row[0].Value, query[0].Value = "mutated", "mutated" }
	p := queries.Parameters{Paging: queries.Paging{IsPaged: true, Page: 2, Size: 2}, Sorting: queries.Sorting{Field: "Name", Direction: queries.Descending}}
	result, err := runSnapshot(t, t.Context(), r, query, p)
	if err != nil {
		t.Fatal(err)
	}
	data, present := result.Data()
	if !present || len(data) != 2 || data[0].ID != 2 || data[1].ID != 3 || result.Details().Paging.TotalItems != 7 {
		t.Fatalf("data %+v, details %+v", data, result.Details())
	}
	if calls != 1 || fake.counts != 1 || fake.finds != 1 || fake.cursor.closes != 1 || !bytes.Equal(fake.countFilter, fake.findFilter) {
		t.Fatal("filter/call/cleanup mismatch")
	}
	want := rawDocument(t, bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "Owner", Value: "allowed"}}, bson.D{{Key: "Owner", Value: "denied"}, {Key: "$or", Value: bson.A{bson.D{}}}}}}})
	if !bytes.Equal(fake.countFilter, want) {
		t.Fatal("authorization overwritten or borrowed filter retained")
	}
	if fake.window.skip != 4 || fake.window.limit != 2 || !reflect.DeepEqual(fake.window.sort, bson.D{{Key: "Name", Value: int32(-1)}, {Key: "_id", Value: int32(1)}}) {
		t.Fatalf("window %+v", fake.window)
	}
}

func TestUnauthorizedRowsNeverContributeToCount(t *testing.T) {
	row := bson.D{{Key: "Owner", Value: "allowed"}}
	fake := &recordingCollection{cursor: &recordingCursor{documents: []bson.Raw{rawAuthor(t, 1, "authorized")}}}
	// The consuming fake checks the renderer's actual frozen match against a
	// fixture containing both visible and hidden rows. This is not evidence of
	// MongoDB server semantics; the live-provider contract is a later checkpoint.
	fake.afterCount = func(context.Context) {
		predicates := fake.countFilter.Lookup("$and").Array()
		authorization := predicates.Index(0).Document().Lookup("Owner").StringValue()
		selection := predicates.Index(1).Document()
		if len(selection) != 5 {
			t.Fatal("unexpected empty selection")
		}
		for _, owner := range []string{"allowed", "denied", "denied"} {
			if owner == authorization {
				fake.total++
			}
		}
	}
	r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{RowFilter: func(context.Context, queries.QueryContext) (bson.D, error) { return row, nil }}, fake)
	result, err := runSnapshot(t, t.Context(), r, bson.D{}, queries.Parameters{Paging: queries.Paging{IsPaged: true, Size: 1}})
	if err != nil || result.Details().Paging.TotalItems != 1 {
		t.Fatalf("authorized total %+v, %v", result.Details(), err)
	}
}

func TestOperationBudgetIncludesCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &recordingCollection{}
		fake.afterCount = func(ctx context.Context) { <-ctx.Done() }
		r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{}, fake)
		started := time.Now()
		result, err := r.Execute(t.Context(), Find[author]{}, queries.QueryContext{})
		if !errors.Is(err, context.DeadlineExceeded) || result.Data != nil || result.TotalItems != 0 || fake.finds != 0 || time.Since(started) != 10*time.Second {
			t.Fatalf("budget elapsed %v, error %v", time.Since(started), err)
		}
	})
}

func TestNilRowFilterDeniesAndExplicitEmptyAllows(t *testing.T) {
	for _, denied := range []bool{true, false} {
		t.Run(map[bool]string{true: "denied", false: "explicit empty"}[denied], func(t *testing.T) {
			fake := &recordingCollection{cursor: &recordingCursor{}}
			r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{RowFilter: func(context.Context, queries.QueryContext) (bson.D, error) {
				if denied {
					return nil, nil
				}
				return bson.D{}, nil
			}}, fake)
			result, err := runSnapshot(t, t.Context(), r, nil, queries.Parameters{})
			if denied {
				noPublication(t, result, err)
				if fake.counts != 0 {
					t.Fatal("denial issued commands")
				}
				return
			}
			data, present := result.Data()
			if err != nil || !present || data == nil || len(data) != 0 || fake.cursor.closes != 1 {
				t.Fatalf("empty %+v, %v", data, err)
			}
		})
	}
}

func TestRendererControlsAndSafeSorts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		parameters  queries.Parameters
		wantSort    bson.D
		skip, limit int64
		invalid     bool
	}{
		{name: "defaults", wantSort: bson.D{{Key: "_id", Value: int32(1)}}, limit: 1001},
		{name: "id tie not duplicated", parameters: queries.Parameters{Sorting: queries.Sorting{Field: "Id", Direction: queries.Descending}}, wantSort: bson.D{{Key: "_id", Value: int32(-1)}}, limit: 1001},
		{name: "exact alias", parameters: queries.Parameters{Sorting: queries.Sorting{Field: "name", Direction: queries.Ascending}}, wantSort: bson.D{{Key: "Name", Value: int32(1)}, {Key: "_id", Value: int32(1)}}, limit: 1001},
		{name: "inactive", parameters: queries.Parameters{Sorting: queries.Sorting{Field: "$where", Direction: queries.Unspecified}}, wantSort: bson.D{{Key: "_id", Value: int32(1)}}, limit: 1001},
		{name: "clamped out of range", parameters: queries.Parameters{Paging: queries.Paging{IsPaged: true, Page: math.MaxInt32, Size: 2}}, wantSort: bson.D{{Key: "_id", Value: int32(1)}}, skip: math.MaxInt32, limit: 2},
		{name: "negative page", parameters: queries.Parameters{Paging: queries.Paging{IsPaged: true, Page: -1, Size: 1}}, invalid: true},
		{name: "zero page size", parameters: queries.Parameters{Paging: queries.Paging{IsPaged: true, Size: 0}}, invalid: true},
		{name: "injection", parameters: queries.Parameters{Sorting: queries.Sorting{Field: "Name.$where", Direction: queries.Ascending}}, invalid: true},
		{name: "wrong direction", parameters: queries.Parameters{Sorting: queries.Sorting{Field: "Name", Direction: 42}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &recordingCollection{total: 0, cursor: &recordingCursor{}}
			r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{}, fake)
			result, err := runSnapshot(t, t.Context(), r, nil, tc.parameters)
			if tc.invalid {
				noPublication(t, result, err)
				if fake.counts != 0 {
					t.Fatal("invalid controls issued commands")
				}
				if err != nil && tc.parameters.Sorting.Field != "" && strings.Contains(err.Error(), string(tc.parameters.Sorting.Field)) {
					t.Fatal("unsafe sort error")
				}
				return
			}
			if err != nil || fake.window.skip != tc.skip || fake.window.limit != tc.limit || !reflect.DeepEqual(fake.window.sort, tc.wantSort) {
				t.Fatalf("window %+v, error %v", fake.window, err)
			}
		})
	}
}

func TestRendererBoundsAndCountConversion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		total     int64
		documents []bson.Raw
		config    RendererOptions[author]
		paging    queries.Paging
		want      error
	}{
		{name: "overlarge page", config: RendererOptions[author]{MaxItems: 1}, paging: queries.Paging{IsPaged: true, Size: 2}, want: ErrLimit},
		{name: "unpaged count", total: 2, config: RendererOptions[author]{MaxItems: 1}, want: ErrLimit},
		{name: "concurrent growth", total: 1, documents: []bson.Raw{rawAuthor(t, 1, "A"), rawAuthor(t, 2, "B")}, config: RendererOptions[author]{MaxItems: 1}, want: ErrLimit},
		{name: "bytes", total: 1, documents: []bson.Raw{rawAuthor(t, 1, "A")}, config: RendererOptions[author]{MaxBSONBytes: 1}, want: ErrLimit},
		{name: "negative count", total: -1, want: ErrValue},
		{name: "page count overflow", total: math.MaxInt64, paging: queries.Paging{IsPaged: true, Size: 1}, want: ErrLimit},
		{name: "largest representable page count", total: math.MaxInt32, paging: queries.Paging{IsPaged: true, Size: 1}},
		{name: "int64 total with representable pages", total: int64(math.MaxInt32) * 2, paging: queries.Paging{IsPaged: true, Size: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &recordingCollection{total: tc.total, cursor: &recordingCursor{documents: tc.documents}}
			r := fakeRenderer(t, ApplicationOwned, tc.config, fake)
			result, err := runSnapshot(t, t.Context(), r, nil, queries.Parameters{Paging: tc.paging})
			if tc.want != nil {
				noPublication(t, result, err)
				if !errors.Is(err, tc.want) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || result.Details().Paging.TotalItems != tc.total || result.Details().Paging.TotalPages() != math.MaxInt32 {
				t.Fatalf("details %+v, %v", result.Details(), err)
			}
		})
	}
}

func TestFailuresSuppressAllPublicationAndCloseAcquiredCursor(t *testing.T) {
	for _, stage := range []string{"count", "find", "getMore", "decode", "close", "next panic", "close panic", "release panic", "cancel after count", "cancel during iteration", "cancel in release", "cancel in close"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cursor := &recordingCursor{documents: []bson.Raw{rawAuthor(t, 1, "A"), rawAuthor(t, 2, "B")}}
			fake := &recordingCollection{total: 2, cursor: cursor}
			ownership := ApplicationOwned
			config := RendererOptions[author]{}
			switch stage {
			case "count":
				fake.countErr = errBoundary
			case "find":
				fake.findErr = errBoundary
			case "getMore":
				cursor.iterationErr = errBoundary
			case "decode":
				cursor.documents[1] = rawDocument(t, bson.D{{Key: "_id", Value: int32(2)}, {Key: "Name", Value: bson.D{}}})
			case "close":
				cursor.closeErr = errBoundary
			case "next panic":
				cursor.panicNext = true
			case "close panic":
				cursor.panicClose = true
			case "release panic":
				ownership = ChronicleOwned
				config.Release = func(context.Context, []bson.Raw) ([]author, error) { panic("secret release panic") }
			case "cancel after count":
				fake.afterCount = func(context.Context) { cancel() }
			case "cancel during iteration":
				cursor.beforeNext = func(_ context.Context, index int) {
					if index == 1 {
						cancel()
					}
				}
			case "cancel in release":
				ownership = ChronicleOwned
				config.Release = func(context.Context, []bson.Raw) ([]author, error) { cancel(); return []author{{ID: 1}, {ID: 2}}, nil }
			case "cancel in close":
				cursor.closeCall = func(context.Context) { cancel() }
			}
			priorClose := cursor.closeCall
			cursor.closeCall = func(cleanup context.Context) {
				if cleanup.Err() != nil {
					t.Error("cleanup inherited cancellation")
				}
				deadline, ok := cleanup.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("unbounded cleanup")
				}
				if priorClose != nil {
					priorClose(cleanup)
				}
			}
			r := fakeRenderer(t, ownership, config, fake)
			result, err := runSnapshot(t, ctx, r, nil, queries.Parameters{Paging: queries.Paging{IsPaged: true, Size: 2}})
			noPublication(t, result, err)
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sensitive") {
				t.Fatal("unsafe error message")
			}
			if stage != "count" && stage != "cancel after count" && cursor.closes != 1 {
				t.Fatalf("closes %d", cursor.closes)
			}
			if strings.HasPrefix(stage, "cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestCleanupDeadlineAndFinalCancellationSuppressSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cursor := &recordingCursor{}
		cursor.closeCall = func(ctx context.Context) { <-ctx.Done() }
		r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{}, &recordingCollection{cursor: cursor})
		started := time.Now()
		result, err := r.Execute(t.Context(), Find[author]{}, queries.QueryContext{})
		if !errors.Is(err, context.DeadlineExceeded) || result.Data != nil || result.TotalItems != 0 || time.Since(started) != 5*time.Second {
			t.Fatalf("cleanup elapsed %v, result %+v, error %v", time.Since(started), result, err)
		}
	})
}

func TestAuthorizationErrorsAndPanicsIssueNoCommands(t *testing.T) {
	for _, panics := range []bool{false, true} {
		fake := &recordingCollection{}
		r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{RowFilter: func(context.Context, queries.QueryContext) (bson.D, error) {
			if panics {
				panic("secret row authorization")
			}
			return bson.D{}, errBoundary
		}}, fake)
		result, err := r.Execute(t.Context(), Find[author]{}, queries.QueryContext{})
		if err == nil || result.Data != nil || result.TotalItems != 0 || fake.counts != 0 || strings.Contains(err.Error(), "secret") {
			t.Fatalf("result %+v, error %v", result, err)
		}
	}
}

func TestInvalidCursorDocumentAndAbsentCursorFailClosed(t *testing.T) {
	for _, fake := range []*recordingCollection{{}, {cursor: &recordingCursor{documents: []bson.Raw{{1, 2, 3}}}}, {cursor: &recordingCursor{documents: []bson.Raw{rawDocument(t, bson.D{{Key: "Name", Value: "missing id"}})}}}} {
		r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{}, fake)
		result, err := r.Execute(t.Context(), Find[author]{}, queries.QueryContext{})
		if !errors.Is(err, ErrValue) || result.Data != nil || result.TotalItems != 0 {
			t.Fatalf("result %+v, error %v", result, err)
		}
		if fake.cursor != nil && fake.cursor.closes != 1 {
			t.Fatal("invalid document leaked cursor")
		}
	}
}

func TestPrimaryAndCleanupErrorsRemainInspectable(t *testing.T) {
	cleanupError := errors.New("cleanup failure")
	fake := &recordingCollection{cursor: &recordingCursor{iterationErr: errBoundary, closeErr: cleanupError}}
	r := fakeRenderer(t, ApplicationOwned, RendererOptions[author]{}, fake)
	result, err := r.Execute(t.Context(), Find[author]{}, queries.QueryContext{})
	if !errors.Is(err, errBoundary) || !errors.Is(err, cleanupError) || result.Data != nil || result.TotalItems != 0 {
		t.Fatalf("result %+v, error %v", result, err)
	}
}

func TestChronicleReleaseRetainsCiphertextAndPrecedesInterception(t *testing.T) {
	for _, mode := range []string{"valid", "error", "partial", "reordered", "identity", "mutation"} {
		t.Run(mode, func(t *testing.T) {
			documents := []bson.Raw{
				rawDocument(t, bson.D{{Key: "_id", Value: int32(1)}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{1, 2, 3}}}, {Key: "lineage", Value: bson.D{{Key: "subject", Value: "secret"}}}}),
				rawDocument(t, bson.D{{Key: "_id", Value: int32(2)}, {Key: "Name", Value: bson.Binary{Subtype: 6, Data: []byte{4, 5, 6}}}}),
			}
			fake := &recordingCollection{total: 2, cursor: &recordingCursor{documents: documents}}
			released, intercepted := false, 0
			r := fakeRenderer(t, ChronicleOwned, RendererOptions[author]{Release: func(_ context.Context, raw []bson.Raw) ([]author, error) {
				released = true
				if len(raw) != 2 || !bytes.Equal(raw[0], documents[0]) || !bytes.Equal(raw[1], documents[1]) {
					t.Fatal("incomplete or borrowed BSON")
				}
				data := []author{{ID: 1, Name: "released A"}, {ID: 2, Name: "released B"}}
				switch mode {
				case "error":
					return data, errBoundary
				case "partial":
					return data[:1], nil
				case "reordered":
					return []author{data[1], data[0]}, nil
				case "identity":
					data[0].ID = 7
				case "mutation":
					raw[0] = rawAuthor(t, 7, "changed")
					data[0].ID = 7
				}
				return data, nil
			}}, fake)
			pipeline := snapshotPipeline(t, r, nil, func(registry *queries.Registry) {
				err := queries.RegisterReadModelInterceptor[author](registry, "after release", func(context.Context, *execution.Scope) (queries.ReadModelInterceptor[author], error) {
					return queries.InterceptorFunc[author](func(_ context.Context, a author) (author, error) {
						if !released || !strings.HasPrefix(a.Name, "released") {
							t.Fatal("intercepted before release")
						}
						intercepted++
						return a, nil
					}), nil
				})
				if err != nil {
					t.Fatal(err)
				}
			})
			result, err := queries.Perform[[]author](t.Context(), pipeline, "author.Snapshot", queries.Request{})
			if mode != "valid" {
				noPublication(t, result, err)
				if intercepted != 0 {
					t.Fatal("failed release reached interceptors")
				}
				return
			}
			data, present := result.Data()
			if err != nil || !present || len(data) != 2 || intercepted != 2 || fake.cursor.closes != 1 {
				t.Fatalf("data %+v, error %v", data, err)
			}
		})
	}
}
