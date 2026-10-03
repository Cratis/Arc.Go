// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package arc

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/authorization"
	"github.com/cratis/arc.go/correlation"
	"github.com/cratis/arc.go/execution"
	"github.com/cratis/arc.go/metadata"
	"github.com/cratis/arc.go/queries"
	"github.com/cratis/arc.go/tenancy"
	di "github.com/cratis/fundamentals.go/dependencyinjection"
)

// Options configures composition. Collaborators are borrowed and must support
// concurrent calls. Configuration slices and pointed-to values are copied.
type Options struct {
	Namespace              string
	Environment            string
	Routes                 *metadata.Options
	Authentication         []authentication.Handler
	Tenancy                tenancy.Options
	TenantResolver         tenancy.Resolver
	Membership             tenancy.Membership
	RequireTenant          bool
	Authorization          authorization.Options
	OpenResources          execution.OpenResources
	ScopeFactory           di.ScopeFactory
	DependencyCatalog      di.Catalog
	Clock                  func() time.Time
	CleanupTimeout         time.Duration
	ExposeExceptionDetails bool
	Logger                 *slog.Logger
	HTTP                   HTTPOptions
	Observable             ObservableOptions
	Introspection          IntrospectionOptions
	Identity               IdentityOptions
}

// ObservableOptions bounds owned query observations. Zero fields select defaults.
// Limits include opening and retired-but-unjoined operations, not just active streams.
// Transport-specific connection/writer limits are added with their transports.
type ObservableOptions struct {
	// MaxObservations is the application-wide operation ceiling; default 1024.
	MaxObservations int
	// MaximumWait is the maximum first-result wait budget; default five minutes.
	MaximumWait time.Duration
	// CloseGrace bounds initial stream cleanup; default five seconds. Timeouts
	// remain owned and must be joined by a later application Shutdown.
	CloseGrace time.Duration
}

// HTTPOptions controls bounded unary HTTP publication and owned-server timeouts.
// Zero fields select defaults; embedded servers own their transport timeouts.
type HTTPOptions struct {
	MaxBodyBytes      int64
	MaxQueryBytes     int
	MaxResponseBytes  int64
	CorrelationHeader string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	ShutdownTimeout   time.Duration
	QueryReaders      []queries.RequestReader
}

// IntrospectionOptions controls discovery exposure. Enabled controls only catalogs.
// Development defaults anonymous; other environments require authentication or
// leave discovery unmapped when no authentication adapter is registered.
type IntrospectionOptions struct {
	Enabled               *bool
	RequireAuthentication *bool
	Roles                 []string
}

// IdentityOptions selects a registered details provider when more than one exists.
type IdentityOptions struct{ DetailsProvider string }

func normalizeOptions(o Options) (Options, error) {
	if o.Environment == "" {
		o.Environment = "Production"
	}
	if o.Routes == nil {
		routes := metadata.DefaultOptions()
		o.Routes = &routes
	} else {
		routes := *o.Routes
		o.Routes = &routes
	}
	o.Authentication = slices.Clone(o.Authentication)
	o.HTTP.QueryReaders = slices.Clone(o.HTTP.QueryReaders)
	o.Introspection.Roles = slices.Clone(o.Introspection.Roles)
	if o.Introspection.Enabled != nil {
		v := *o.Introspection.Enabled
		o.Introspection.Enabled = &v
	}
	if o.Introspection.RequireAuthentication != nil {
		v := *o.Introspection.RequireAuthentication
		o.Introspection.RequireAuthentication = &v
	}
	if o.Authorization.Fallback != nil {
		c := cloneCatalog(metadata.Catalog{Version: metadata.Version, Commands: []metadata.Command{{Authorization: o.Authorization.Fallback}}})
		o.Authorization.Fallback = c.Commands[0].Authorization
	}
	if o.OpenResources != nil && o.ScopeFactory != nil || o.CleanupTimeout < 0 || o.TenantResolver != nil && o.Tenancy != (tenancy.Options{}) {
		return Options{}, ErrInvalidOptions
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.CleanupTimeout == 0 {
		o.CleanupTimeout = 30 * time.Second
	}
	observable := &o.Observable
	if observable.MaxObservations < 0 || observable.MaximumWait < 0 || observable.CloseGrace < 0 {
		return Options{}, ErrInvalidOptions
	}
	if observable.MaxObservations == 0 {
		observable.MaxObservations = 1024
	}
	if observable.MaximumWait == 0 {
		observable.MaximumWait = 5 * time.Minute
	}
	if observable.CloseGrace == 0 {
		observable.CloseGrace = 5 * time.Second
	}
	h := &o.HTTP
	if h.MaxBodyBytes < 0 || h.MaxQueryBytes < 0 || h.MaxResponseBytes < 0 || h.MaxHeaderBytes < 0 || h.ReadHeaderTimeout < 0 || h.ReadTimeout < 0 || h.WriteTimeout < 0 || h.IdleTimeout < 0 || h.ShutdownTimeout < 0 {
		return Options{}, ErrInvalidOptions
	}
	if h.MaxBodyBytes == 0 {
		h.MaxBodyBytes = 1 << 20
	}
	if h.MaxQueryBytes == 0 {
		h.MaxQueryBytes = 8 << 10
	}
	if h.MaxResponseBytes == 0 {
		h.MaxResponseBytes = 16 << 20
	}
	if h.MaxHeaderBytes == 0 {
		h.MaxHeaderBytes = 1 << 20
	}
	if h.ReadHeaderTimeout == 0 {
		h.ReadHeaderTimeout = 5 * time.Second
	}
	if h.ReadTimeout == 0 {
		h.ReadTimeout = 30 * time.Second
	}
	if h.WriteTimeout == 0 {
		h.WriteTimeout = 30 * time.Second
	}
	if h.IdleTimeout == 0 {
		h.IdleTimeout = 60 * time.Second
	}
	if h.ShutdownTimeout == 0 {
		h.ShutdownTimeout = 30 * time.Second
	}
	if h.CorrelationHeader == "" {
		h.CorrelationHeader = correlation.DefaultHeader
	}
	if !headerToken(h.CorrelationHeader) {
		return Options{}, ErrInvalidOptions
	}
	for _, controlled := range []string{"Content-Type", "Content-Length", "Cache-Control", "Vary", "Allow", "Set-Cookie", "Authorization", "X-Allowed-Severity", "x-cratis-tenant-id", "Content-Encoding", "Connection", "Transfer-Encoding", "Host"} {
		if strings.EqualFold(h.CorrelationHeader, controlled) {
			return Options{}, ErrInvalidOptions
		}
	}
	if o.TenantResolver == nil {
		var err error
		o.TenantResolver, err = tenancy.NewResolver(o.Tenancy)
		if err != nil {
			return Options{}, err
		}
	}
	if o.ScopeFactory != nil {
		if nilValue(o.ScopeFactory) {
			return Options{}, ErrInvalidOptions
		}
		o.OpenResources = execution.ResourcesFrom(o.ScopeFactory)
		if o.DependencyCatalog == nil {
			o.DependencyCatalog, _ = o.ScopeFactory.(di.Catalog)
		}
	}
	if nilValue(o.TenantResolver) || o.Membership != nil && nilValue(o.Membership) || o.DependencyCatalog != nil && nilValue(o.DependencyCatalog) {
		return Options{}, ErrInvalidOptions
	}
	if _, err := authentication.New(o.Authentication...); err != nil {
		return Options{}, err
	}
	return o, nil
}

func headerToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c) {
			continue
		}
		return false
	}
	return http.CanonicalHeaderKey(s) != ""
}
