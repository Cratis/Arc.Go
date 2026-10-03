// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming

import "testing"

func TestWSControlsUsePayloadAndLexicalRevisions(t *testing.T) {
	valid := []string{
		`{"type":"Subscribe","queryId":"q1","revision":1,"payload":{"queryName":"Tasks.All","arguments":{"a":null,"b":"text"}}}`,
		`{"type":"Subscribe","queryId":"q1","revision":null,"payload":{"queryName":"Tasks.All"}}`,
		`{"type":"Unsubscribe","queryId":"q1","revision":9007199254740991,"payload":false}`,
		`{"type":"Ping","timestamp":1700000000123}`,
		`{"type":"pong","timestamp":null}`,
	}
	for _, body := range valid {
		if _, err := ParseWSControl([]byte(body), true); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	}
	invalid := []string{
		`{"type":"Subscribe","queryId":"q1","request":{"queryName":"Tasks.All"}}`,
		`{"type":"Subscribe","queryId":"q1","revision":1e0,"payload":{"queryName":"Tasks.All"}}`,
		`{"type":"Subscribe","queryId":"q1","revision":0,"payload":{"queryName":"Tasks.All"}}`,
		`{"type":"Subscribe","queryId":"q1","revision":9007199254740992,"payload":{"queryName":"Tasks.All"}}`,
		`{"type":"Unsubscribe","queryId":"q1","Revision":1,"revision":2}`,
		`{"type":"Unsubscribe","queryId":"q1","revision":1,"reviſion":2}`,
		`{"type":"Ping","timestamp":1.5}`,
		`{"type":"Ping","timestamp":"1"}`,
		`{"type":"Ping"} {}`,
		`{"type":"Unknown"}`,
	}
	for _, body := range invalid {
		if _, err := ParseWSControl([]byte(body), true); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if _, err := ParseWSControl([]byte(valid[0]), false); err == nil {
		t.Fatal("direct socket accepted subscription control")
	}
}

func TestSSEControlsRejectUnicodeFoldedRevisionDuplicates(t *testing.T) {
	_, err := ParseSSEControl([]byte(`{"connectionId":"c1","queryId":"q1","revision":1,"reviſion":2}`), false)
	if err == nil {
		t.Fatal("Unicode simple-fold duplicate overrode SSE revision")
	}
}

func FuzzWSControl(f *testing.F) {
	f.Add([]byte(`{"type":"Ping","timestamp":1700000000123}`))
	f.Add([]byte(`{"type":"Subscribe","queryId":"q1","revision":1,"payload":{"queryName":"Tasks.All"}}`))
	f.Add([]byte(`{"type":"Unsubscribe","queryId":"q1","revision":null}`))
	f.Fuzz(func(_ *testing.T, body []byte) { _, _ = ParseWSControl(body, true); _, _ = ParseWSControl(body, false) })
}
