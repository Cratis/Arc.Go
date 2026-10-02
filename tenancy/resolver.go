// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cratis/arc.go/identity"
)

var (
	// ErrInvalidOptions identifies malformed selector configuration.
	ErrInvalidOptions = errors.New("invalid tenant resolver options")
	// ErrAmbiguousSelection identifies multiple selector values.
	ErrAmbiguousSelection = errors.New("ambiguous tenant selection")
	// ErrUnsupportedHost identifies non-ASCII host/base-domain input.
	ErrUnsupportedHost = errors.New("non-ASCII tenant host unsupported")
	// ErrNotSet identifies a required but absent tenant.
	ErrNotSet = errors.New("tenant not set")
)

// Strategy selects exactly one source; it does not establish membership.
type Strategy uint8

const (
	// Header selects a request header; this is the zero/default strategy.
	Header Strategy = iota
	// Query selects a query-string parameter.
	Query
	// Claim selects the first matching authenticated principal claim.
	Claim
	// Subdomain selects one label before BaseDomain, else the header.
	Subdomain
	// Fixed selects deployment configuration independently of the request.
	Fixed
)

// Options is copied by NewResolver. Empty selector names use defaults.
type Options struct {
	// Strategy selects the tenant source.
	Strategy Strategy
	// Header names the header selector and subdomain fallback.
	Header string
	// QueryParameter names the query selector.
	QueryParameter string
	// ClaimType names the verified claim selector.
	ClaimType string
	// BaseDomain is required for Subdomain; ASCII/punycode only.
	BaseDomain string
	// FixedID is used by Fixed; zero selects development.
	FixedID ID
}

// DefaultOptions returns header tenancy and independent selector defaults.
func DefaultOptions() Options {
	return Options{Header: "x-cratis-tenant-id", QueryParameter: "tenantId", ClaimType: "tenant_id", FixedID: ID{text: "development"}}
}

// Resolver selects a tenant synchronously without authorizing membership. Request
// data is borrowed for the call; shared resolvers must support concurrent calls.
type Resolver interface {
	Resolve(context.Context, *http.Request) (ID, error)
}

// ResolverFunc adapts a tenant selector callback.
type ResolverFunc func(context.Context, *http.Request) (ID, error)

// Resolve invokes f with borrowed request data.
func (f ResolverFunc) Resolve(ctx context.Context, request *http.Request) (ID, error) {
	return f(ctx, request)
}

type selector struct {
	options    Options
	baseDomain string
}

// NewResolver copies/validates options without reading any request. The result is
// immutable and concurrent-safe. It never trusts forwarded host/identity headers.
func NewResolver(options Options) (Resolver, error) {
	defaults := DefaultOptions()
	if options.Header == "" {
		options.Header = defaults.Header
	}
	if options.QueryParameter == "" {
		options.QueryParameter = defaults.QueryParameter
	}
	if options.ClaimType == "" {
		options.ClaimType = defaults.ClaimType
	}
	if !options.FixedID.IsSet() {
		options.FixedID = defaults.FixedID
	}
	if options.Strategy > Fixed || !headerName(options.Header) || !selectorName(options.QueryParameter) || !selectorName(options.ClaimType) {
		return nil, ErrInvalidOptions
	}
	base := ""
	if options.Strategy == Subdomain {
		var err error
		base, err = normalizeHost(options.BaseDomain)
		if err != nil {
			return nil, errors.Join(ErrInvalidOptions, err)
		}
		if base == "" || len(strings.Split(base, ".")) < 2 {
			return nil, ErrInvalidOptions
		}
	}
	return &selector{options: options, baseDomain: base}, nil
}
func (s *selector) Resolve(ctx context.Context, request *http.Request) (ID, error) {
	if ctx == nil {
		return ID{}, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return ID{}, err
	}
	switch s.options.Strategy {
	case Fixed:
		return s.options.FixedID, nil
	case Claim:
		principal, _ := identity.PrincipalFrom(ctx)
		if !principal.IsAuthenticated() {
			return ID{}, nil
		}
		text, _ := principal.Claim(s.options.ClaimType)
		return ParseID(text)
	}
	if request == nil {
		return ID{}, nil
	}
	switch s.options.Strategy {
	case Header:
		return selectHeader(request, s.options.Header)
	case Query:
		if request.URL == nil {
			return ID{}, nil
		}
		values, err := url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			return ID{}, ErrInvalidID
		}
		return selectValues(values[s.options.QueryParameter])
	case Subdomain:
		host, err := normalizeHost(request.Host)
		if err != nil {
			return ID{}, err
		}
		suffix := "." + s.baseDomain
		if strings.HasSuffix(host, suffix) {
			label := strings.TrimSuffix(host, suffix)
			if dnsLabel(label) {
				return ParseID(label)
			}
		}
		return selectHeader(request, s.options.Header)
	}
	return ID{}, ErrInvalidOptions
}
func selectHeader(request *http.Request, name string) (ID, error) {
	var values []string
	for key, entries := range request.Header {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	return selectValues(values)
}
func selectValues(values []string) (ID, error) {
	if len(values) > 1 {
		return ID{}, ErrAmbiguousSelection
	}
	if len(values) == 0 {
		return ID{}, nil
	}
	return ParseID(values[0])
}
func selectorName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	for _, c := range name {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func headerName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c) {
			continue
		}
		return false
	}
	return true
}
