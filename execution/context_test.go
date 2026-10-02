// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package execution_test

import (
	"context"
	"testing"
	"time"

	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/tenancy"
)

func TestReceiptNormalizationAndOverrides(t *testing.T) {
	root := context.Background()
	if _, ok := execution.ReceivedAt(root); ok {
		t.Fatal("absent")
	}
	now := time.Now()
	parent := execution.WithReceivedAt(root, now)
	got, ok := execution.ReceivedAt(parent)
	if !ok || got.Location() != time.UTC || got != now.Round(0).UTC() {
		t.Fatal(got)
	}
	child := execution.WithReceivedAt(parent, time.Time{})
	if got, ok := execution.ReceivedAt(child); !ok || !got.IsZero() {
		t.Fatal("explicit zero")
	}
	if got, _ := execution.ReceivedAt(parent); !got.Equal(now) {
		t.Fatal("parent changed")
	}
	offset := time.Date(2025, 1, 2, 3, 4, 5, 6, time.FixedZone("offset", 3600))
	if got, _ := execution.ReceivedAt(execution.WithReceivedAt(root, offset)); got.Location() != time.UTC || !got.Equal(offset) {
		t.Fatal(got)
	}
}
func TestExplicitMetadata(t *testing.T) {
	parent := identity.WithPrincipal(context.Background(), identity.System("admin"))
	parent = tenancy.WithTenant(parent, tenancy.Default())
	ctx, err := execution.NewContext(parent, execution.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	m := execution.Capture(ctx)
	if m.Principal.IsAuthenticated() || m.Tenant.IsSet() || m.CorrelationID.IsZero() || m.ReceivedAt.IsZero() {
		t.Fatal("inherited authority or missing defaults")
	}
	if _, ok := identity.PrincipalFrom(ctx); !ok {
		t.Fatal("anonymous not explicit")
	}
	if _, ok := tenancy.TenantFrom(ctx); !ok {
		t.Fatal("NotSet not explicit")
	}
	m.Principal = identity.System("jobs")
	m.Tenant = tenancy.Default()
	other, err := execution.NewContext(parent, m)
	if err != nil {
		t.Fatal(err)
	}
	captured := execution.Capture(other)
	if !captured.Principal.Equal(m.Principal) || captured.Tenant != m.Tenant || captured.CorrelationID != m.CorrelationID || !captured.ReceivedAt.Equal(m.ReceivedAt) {
		t.Fatal("capture")
	}
	if correlation.FromContext(ctx) != m.CorrelationID {
		t.Fatal("context changed")
	}
}
