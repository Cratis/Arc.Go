// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cratis/arc.go/internal/streaming"
	"github.com/cratis/arc.go/queries"
)

func TestHubFlatControlsAndTextNullArguments(t *testing.T) {
	text, field, direction, mode := "Ada", "title", "DeSc", "FuLl"
	page, size := int32(2), int32(25)
	request, transfer, err := hubRequest(streaming.SubscriptionRequest{QueryName: "Task.All", Arguments: map[string]*string{"prefix": &text, "optional": nil}, Page: &page, PageSize: &size, SortBy: &field, SortDirection: &direction, TransferMode: &mode})
	if err != nil || transfer != queries.Full || request.Parameters().Paging.Page != 2 || request.Parameters().Paging.Size != 25 || request.Parameters().Sorting.Direction != queries.Descending {
		t.Fatal(request, transfer, err)
	}
	text = "changed"
	if got, present := request.Arguments().Get("prefix"); !present || got != "Ada" {
		t.Fatal(got, present)
	}
	if got, present := request.Arguments().Get("optional"); !present || got != nil {
		t.Fatal(got, present)
	}
	request, transfer, err = hubRequest(streaming.SubscriptionRequest{SortBy: &field})
	if err != nil || transfer != queries.Legacy || request.Parameters().Sorting.Direction != queries.Unspecified {
		t.Fatal(request, transfer, err)
	}
	direction, mode = "invalid", "unknown"
	request, transfer, err = hubRequest(streaming.SubscriptionRequest{SortBy: &field, SortDirection: &direction, TransferMode: &mode})
	if err != nil || transfer != queries.Legacy || request.Parameters().Sorting.Direction != queries.Ascending {
		t.Fatal(request, transfer, err)
	}
}
func TestHubOptionsValidateOriginsAndLimits(t *testing.T) {
	for _, options := range []ObservableOptions{
		{MaxConnections: -1}, {MaxConnectionsPerOwner: -1}, {MaxSubscriptions: -1}, {MaxOpenings: -1}, {MaxOpeningsPerConnection: -1}, {MaxQueryIDs: -1}, {MaxOutboundJobs: -1}, {MaxQueuedBytes: -1}, {MaxStreamingBytes: -1}, {KeepAliveInterval: -1}, {ConnectionLifetime: -1},
		{AllowedOrigins: []string{"*"}}, {AllowedOrigins: []string{"https://example.com/"}}, {AllowedOrigins: []string{"https://user@example.com"}},
	} {
		if _, err := NewBuilder(Options{Observable: options}); err == nil {
			t.Fatal(options)
		}
	}
	origins := []string{"https://Example.COM:443", "null"}
	b, err := NewBuilder(Options{Observable: ObservableOptions{AllowedOrigins: origins}})
	if err != nil {
		t.Fatal(err)
	}
	origins[0] = "https://changed.invalid"
	if b.options.Observable.AllowedOrigins[0] != "https://example.com" {
		t.Fatal(b.options.Observable.AllowedOrigins)
	}
}

func TestClosedHubCookieCleanupPreservesLiveEvidence(t *testing.T) {
	b, err := NewBuilder(Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	a.hubs.connections["live"] = &hubConnection{ctx: context.Background()}
	r := httptest.NewRequest("HEAD", hubSSEPath, nil)
	r.AddCookie(&http.Cookie{Name: hubCookiePrefix + "live", Value: "live-secret"})
	r.AddCookie(&http.Cookie{Name: hubCookiePrefix + "closed", Value: "closed-secret"})
	w := httptest.NewRecorder()
	a.expireHubCookies(w, r)
	response := w.Result()
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	cookies := response.Cookies()
	if len(cookies) != 1 || cookies[0].Name != hubCookiePrefix+"closed" || cookies[0].MaxAge != -1 || cookies[0].Path != hubSSEPath || !cookies[0].HttpOnly {
		t.Fatal(cookies)
	}
}
