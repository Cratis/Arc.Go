// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package authentication_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cratis/arc.go/authentication"
)

func TestAuthenticationClonesRequestContextHeadersAndURL(t *testing.T) {
	request := httptest.NewRequest("GET", "/original?value=original", strings.NewReader("shared body"))
	request.Header.Set("X-Original", "original")
	originalContext := request.Context()
	chain, err := authentication.New(authentication.HandlerFunc(func(ctx context.Context, cloned *http.Request) (authentication.Result, error) {
		if cloned == request || cloned.Context() != ctx || cloned.URL == request.URL {
			t.Error("request context or URL not isolated")
		}
		if cloned.Body != request.Body {
			t.Error("body unexpectedly cloned")
		}
		cloned.Header["X-Original"][0] = "changed"
		cloned.Header.Set("X-Added", "changed")
		cloned.URL.Path = "/changed"
		cloned.URL.RawQuery = "value=changed"
		return authentication.Anonymous(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := chain.Authenticate(t.Context(), request); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if request.Context() != originalContext || request.Header.Get("X-Original") != "original" || request.Header.Get("X-Added") != "" || request.URL.Path != "/original" || request.URL.RawQuery != "value=original" {
		t.Fatal("authentication mutated caller request")
	}
}
