// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package streaming_test

import (
	"encoding/json"
	"testing"

	"github.com/cratis/arc.go/internal/streaming"
)

func TestSSEControlsHaveDistinctEnvelopesAndStrictRevisions(t *testing.T) {
	valid := `{"connectionId":"c","queryId":"q","revision":1,"request":{"queryName":"Acme.Task.All","arguments":{"prefix":"Ada","other":null},"page":0,"pageSize":25,"transferMode":"full"}}`
	got, err := streaming.ParseSSEControl([]byte(valid), true)
	if err != nil || got.Revision == nil || *got.Revision != 1 || *got.Request.Arguments["prefix"] != "Ada" || got.Request.Arguments["other"] != nil {
		t.Fatal(got, err)
	}
	if _, err := streaming.ParseSSEControl([]byte(`{"connectionId":"c","queryId":"q","revision":1,"request":17}`), false); err != nil {
		t.Fatal("unsubscribe decoded unknown request field", err)
	}
	for _, revision := range []string{"null", "9007199254740991"} {
		if _, err := streaming.ParseSSEControl([]byte(`{"connectionId":"c","queryId":"q","revision":`+revision+`}`), false); err != nil {
			t.Fatal(err)
		}
	}
	for _, revision := range []string{`"1"`, "1.0", "1e0", "0", "-1", "9007199254740992"} {
		if _, err := streaming.ParseSSEControl([]byte(`{"connectionId":"c","queryId":"q","revision":`+revision+`}`), false); err == nil {
			t.Fatal("accepted", revision)
		}
	}
	for _, body := range []string{
		`null`, `{}`, `{"connectionId":"c","queryId":"q","payload":{"queryName":"x"}}`,
		`{"connectionId":"c","queryId":"q","revision":1,"Revision":null,"request":{"queryName":"x"}}`,
		`{"connectionId":"c","queryId":"q","request":{"queryName":"x","arguments":{"a":"1","A":"2"}}}`,
		`{"connectionId":"c","queryId":"q","request":{"queryName":"x","arguments":{"a":{}}}}`,
		valid + `{}`, valid[:len(valid)-1],
	} {
		if _, err := streaming.ParseSSEControl([]byte(body), true); err == nil {
			t.Fatal("accepted", body)
		}
	}
}
func TestHubMessageOmitsAbsentFields(t *testing.T) {
	body, err := json.Marshal(streaming.Message{Type: "Unauthorized", QueryID: "q"})
	if err != nil || string(body) != `{"type":"Unauthorized","queryId":"q"}` {
		t.Fatal(string(body), err)
	}
}
func FuzzSSEControl(f *testing.F) {
	for _, seed := range []string{`{}`, `null`, `{"connectionId":"c","queryId":"q","revision":1}`, `{"connectionId":"c","queryId":"q","request":{"queryName":"Task.All","arguments":{"a":null}}}`} {
		f.Add([]byte(seed), true)
		f.Add([]byte(seed), false)
	}
	f.Fuzz(func(t *testing.T, body []byte, subscribe bool) {
		if len(body) > 1<<20 {
			return
		}
		control, err := streaming.ParseSSEControl(body, subscribe)
		if err == nil && (control.ConnectionID == "" || control.QueryID == "" || control.Revision != nil && !control.Revision.Valid() || subscribe && control.Request == nil) {
			t.Fatal(control)
		}
	})
}
