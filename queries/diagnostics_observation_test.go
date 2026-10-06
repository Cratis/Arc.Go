// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/observability"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

func diagnosticCount(recorder *observability.Recorder, phase observability.Phase, outcome observability.Outcome) uint64 {
	var count uint64
	for _, metric := range recorder.Snapshot().Metrics {
		if metric.Phase == phase && metric.Outcome == outcome {
			count += metric.Count
		}
	}
	return count
}

func TestObservationDiagnosticsOnlyAcknowledgeAcceptedData(t *testing.T) {
	for _, scenario := range []string{"eof", "suppressed", "denied", "failed delivery", "cancelled delivery", "obsolete delivery", "source failure"} {
		t.Run(scenario, func(t *testing.T) {
			recorder := diagnosticRecorder(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			next, delivered := 0, 0
			source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) {
				return streamFuncs[Item]{next: func(context.Context) (Item, error) {
					next++
					if scenario == "source failure" {
						return Item{}, errors.New("SECRET failure")
					}
					if next > 3 {
						return Item{}, io.EOF
					}
					return Item{Name: "SECRET result"}, nil
				}, close: func(context.Context) error { return nil }}, nil
			})
			r := observableRegistry(t, source)
			mustRegister(t, r.AddEmissionGuard("verdict", func(context.Context, *execution.Scope) (queries.EmissionGuard, error) {
				return queries.EmissionGuardFunc(func(context.Context, queries.EmissionContext) (queries.EmissionVerdict, error) {
					if scenario == "suppressed" {
						return queries.Suppress, nil
					}
					if scenario == "denied" {
						return queries.DenyAndTerminate, nil
					}
					return queries.Allow, nil
				}), nil
			}))
			p := observationPipeline(t, r, queries.PipelineOptions{Diagnostics: recorder, EnableQueryHealth: true})
			o, _, err := p.Open(ctx, "Item.Observe", queries.Request{})
			mustRegister(t, err)
			if o == nil {
				t.Fatal("missing observation")
			}
			err = o.Run(ctx, queries.ObservationOptions{TransferMode: queries.Full}, func(queries.Result[any]) error {
				delivered++
				switch scenario {
				case "failed delivery":
					return errors.New("SECRET rejected delivery")
				case "obsolete delivery":
					return streaming.ErrObsolete
				case "cancelled delivery":
					cancel()
				}
				return nil
			})
			wantOutcome := observability.Success
			wantDelivered := uint64(0)
			switch scenario {
			case "eof":
				wantDelivered = 3
			case "denied":
				wantOutcome = observability.Authorization
			case "failed delivery", "obsolete delivery", "source failure":
				wantOutcome = observability.Error
			case "cancelled delivery":
				wantOutcome = observability.Cancelled
			}
			if (err != nil) != (wantOutcome == observability.Error || wantOutcome == observability.Cancelled) {
				t.Fatal(err)
			}
			health := p.(queries.HealthReporter).QueryHealth()
			if len(health) != 1 || health[0].Delivered != wantDelivered {
				t.Fatal(health)
			}
			mustRegister(t, o.Close(t.Context()))
			mustRegister(t, o.Close(t.Context()))
			if diagnosticCount(recorder, observability.Opening, observability.Success) != 1 || diagnosticCount(recorder, observability.Delivered, observability.Success) != wantDelivered || diagnosticCount(recorder, observability.Consumption, wantOutcome) != 1 || diagnosticCount(recorder, observability.Joined, wantOutcome) != 1 {
				t.Fatal(recorder.Snapshot())
			}
			if len(recorder.Snapshot().Events) > 5 {
				t.Fatal("unbounded stream events")
			}
			if scenario == "suppressed" && delivered != 0 {
				t.Fatal(delivered)
			}
			body, err := json.Marshal(recorder.Snapshot())
			mustRegister(t, err)
			if strings.Contains(string(body), "SECRET") {
				t.Fatal(string(body))
			}
		})
	}
}

func TestObservationDiagnosticsRetainUnknownCleanupUntilActualJoin(t *testing.T) {
	for _, scenario := range []string{"pending", "panic", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := diagnosticRecorder(t)
				closed := false
				source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) {
					return streamFuncs[Item]{next: func(context.Context) (Item, error) { return Item{}, io.EOF }, close: func(ctx context.Context) error {
						if closed {
							return nil
						}
						switch scenario {
						case "pending":
							return observable.ErrJoinPending
						case "panic":
							panic("SECRET close")
						default:
							<-ctx.Done()
							return ctx.Err()
						}
					}}, nil
				})
				p := observationPipeline(t, observableRegistry(t, source), queries.PipelineOptions{Diagnostics: recorder, EnableQueryHealth: true})
				o, _, err := p.Open(t.Context(), "Item.Observe", queries.Request{})
				mustRegister(t, err)
				mustRegister(t, o.Run(t.Context(), queries.ObservationOptions{}, func(queries.Result[any]) error { t.Fatal("EOF delivered"); return nil }))
				budget, cancel := context.WithTimeout(t.Context(), time.Second)
				err = o.Close(budget)
				cancel()
				if err == nil {
					t.Fatal("unknown cleanup reported joined")
				}
				if diagnosticCount(recorder, observability.Joined, observability.Success) != 0 || diagnosticCount(recorder, observability.Joined, observability.Error) != 0 {
					t.Fatal(recorder.Snapshot())
				}
				health := p.(queries.HealthReporter).QueryHealth()
				if len(health) != 1 || health[0].Retained != 1 {
					t.Fatal(health)
				}
				health[0].Retained = 999
				if p.(queries.HealthReporter).QueryHealth()[0].Retained != 1 {
					t.Fatal("snapshot alias")
				}
				closed = true
				err = p.CloseObservations(t.Context())
				if (err != nil) != (scenario == "panic") {
					t.Fatal(err)
				}
				_ = o.Close(t.Context()) // Previously reported close diagnostics are retained.
				if len(p.(queries.HealthReporter).QueryHealth()) != 0 {
					t.Fatal("joined owner retained")
				}
				want := observability.Success
				if scenario == "panic" {
					want = observability.Error
				}
				if diagnosticCount(recorder, observability.Joined, want) != 1 || diagnosticCount(recorder, observability.Cleanup, want) != 1 || diagnosticCount(recorder, observability.Consumption, observability.Success) != 1 {
					t.Fatal(recorder.Snapshot())
				}
			})
		})
	}
}

func TestObservationOpeningPanicDoesNotBecomeCancellationDuringCleanup(t *testing.T) {
	recorder := diagnosticRecorder(t)
	source := sourceFunc[Item](func(context.Context) (observable.Stream[Item], error) { panic("SECRET open") })
	p := observationPipeline(t, observableRegistry(t, source), queries.PipelineOptions{Diagnostics: recorder})
	o, result, err := p.Open(t.Context(), "Item.Observe", queries.Request{})
	if err == nil || o != nil || !result.HasExceptions() {
		t.Fatal(o, result, err)
	}
	if diagnosticCount(recorder, observability.Opening, observability.Error) != 1 || diagnosticCount(recorder, observability.Joined, observability.Error) != 1 {
		t.Fatal(recorder.Snapshot())
	}
}

func TestObservationDiagnosticsCountAcknowledgedDeltaDeliveries(t *testing.T) {
	for _, failSecond := range []bool{false, true} {
		recorder := diagnosticRecorder(t)
		p := observationPipeline(t, deltaRegistry(t), queries.PipelineOptions{Diagnostics: recorder, EnableQueryHealth: true})
		o, _, err := p.Open(t.Context(), "DeltaItem.All", queries.Request{})
		mustRegister(t, err)
		frames := 0
		err = o.Run(t.Context(), queries.ObservationOptions{TransferMode: queries.Delta}, func(queries.Result[any]) error {
			frames++
			if failSecond && frames == 2 {
				return errors.New("SECRET rejected delivery")
			}
			return nil
		})
		want := uint64(2) // Baseline and one change-set-only update; the middle candidate is suppressed.
		if failSecond {
			want = 1
		}
		if (err != nil) != failSecond {
			t.Fatal(err)
		}
		health := p.(queries.HealthReporter).QueryHealth()
		if len(health) != 1 || health[0].Delivered != want || diagnosticCount(recorder, observability.Delivered, observability.Success) != want {
			t.Fatal(failSecond, health, recorder.Snapshot())
		}
		mustRegister(t, o.Close(t.Context()))
	}
}
