// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc_test

import (
	"context"
	"reflect"
	"testing"

	arc "github.com/cratis/arc.go"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/observable"
	"github.com/cratis/arc.go/queries"
)

type HubDeltaItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func assertHubCollection(t *testing.T, message map[string]any, mode string, first bool) {
	t.Helper()
	if message["type"] != "QueryResult" || message["queryId"] != "q" || message["revision"] != float64(1) {
		t.Fatal(message)
	}
	payload := message["payload"].(map[string]any)
	if payload["isSuccess"] != true {
		t.Fatal(payload)
	}
	wantData := first || mode != "delta"
	if _, present := payload["data"]; present != wantData {
		t.Fatalf("data presence mode %s first %v: %v", mode, first, payload)
	}
	wantChanges := mode != "full" && (!first || mode != "delta")
	if _, present := payload["changeSet"]; present != wantChanges {
		t.Fatalf("changes mode %s first %v: %v", mode, first, payload)
	}
	if !wantChanges {
		return
	}
	want := map[string]any{"added": []any{}, "replaced": []any{}, "removed": []any{}}
	if first {
		want["added"] = []any{map[string]any{"id": "a", "title": "old"}, map[string]any{"id": "b", "title": "gone"}}
	} else {
		want["added"] = []any{map[string]any{"id": "c", "title": "new"}}
		want["replaced"] = []any{map[string]any{"id": "a", "title": "changed"}}
		want["removed"] = []any{map[string]any{"id": "b", "title": "gone"}}
	}
	if !reflect.DeepEqual(payload["changeSet"], want) {
		t.Fatalf("changes = %#v; want %#v", payload["changeSet"], want)
	}
}
func TestHubCollectionTransferModesOnBothRealTransports(t *testing.T) {
	for _, transport := range []string{"sse", "ws"} {
		for _, mode := range []string{"full", "delta", "legacy", "unknown"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				b, err := arc.NewBuilder(arc.Options{})
				if err != nil {
					t.Fatal(err)
				}
				state, err := observable.NewState([]HubDeltaItem{{ID: "a", Title: "old"}, {ID: "b", Title: "gone"}}, observable.SubjectOptions[[]HubDeltaItem]{})
				if err != nil {
					t.Fatal(err)
				}
				if err := queries.RegisterObservable[HubDeltaItem](b, "All", queries.Function(func(context.Context, queries.NoArguments) (observable.Source[[]HubDeltaItem], error) {
					return state, nil
				}), queries.WithAuthorization[queries.NoArguments](metadata.Authorization{AllowAnonymous: true})); err != nil {
					t.Fatal(err)
				}
				a, server := startSSEServer(t, b, false)
				request := map[string]any{"queryName": a.Catalog().Queries[0].Identity(), "transferMode": mode}
				var read func() map[string]any
				var replace func()
				if transport == "sse" {
					client := openTestHub(t, server, nil)
					client.control(t, "/subscribe", "q", 1, request, nil, 200)
					read = func() map[string]any { return readSSEResult(t, client.reader) }
					replace = func() {
						client.control(t, "/unsubscribe", "q", 1, nil, nil, 200)
						client.control(t, "/subscribe", "q", 2, request, nil, 200)
					}
				} else {
					client, response := openWireWS(t, server, wsHub, nil)
					if response.StatusCode != 101 {
						t.Fatal(response.StatusCode)
					}
					if message := client.message(t); message["type"] != "Connected" {
						t.Fatal(message)
					}
					client.send(t, map[string]any{"type": "Subscribe", "queryId": "q", "revision": 1, "payload": request})
					read = func() map[string]any { return client.message(t) }
					replace = func() {
						client.send(t, map[string]any{"type": "Unsubscribe", "queryId": "q", "revision": 1})
						client.send(t, map[string]any{"type": "Subscribe", "queryId": "q", "revision": 2, "payload": request})
					}
				}
				assertHubCollection(t, read(), mode, true)
				if err := state.Publish(t.Context(), []HubDeltaItem{{ID: "a", Title: "changed"}, {ID: "c", Title: "new"}}); err != nil {
					t.Fatal(err)
				}
				assertHubCollection(t, read(), mode, false)
				// Equal-revision unsubscribe plus replacement establishes a fresh baseline.
				replace()
				message := read()
				payload := message["payload"].(map[string]any)
				if message["revision"] != float64(2) || payload["data"] == nil || mode == "delta" && payload["changeSet"] != nil {
					t.Fatal("replacement did not reset baseline", message)
				}
			})
		}
	}
}
