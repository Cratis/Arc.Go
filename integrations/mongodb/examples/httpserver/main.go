// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Command httpserver hosts the manual MongoDB snapshot example on loopback.
// Its environment-configured bearer secret is a local demonstration, not an
// identity-platform adapter. Provision rows and indexes before running it.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cratis/arc.go/authentication"
	"github.com/cratis/arc.go/identity"
	"github.com/cratis/arc.go/integrations/mongodb/examples/snapshot"
	"github.com/cratis/arc.go/tenancy"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func run(ctx context.Context, uri, secret, tenantText string) (err error) {
	parsed, parseErr := url.Parse(uri)
	if parseErr != nil || parsed.Scheme != "mongodb" || parsed.Hostname() != "127.0.0.1" || len(secret) < 32 || tenantText == "" {
		return errors.New("this local demo requires a loopback MongoDB URI, a secret of at least 32 bytes and an explicit tenant")
	}
	tenant, err := tenancy.ParseID(tenantText)
	if err != nil {
		return err
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(3 * time.Second))
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, client.Disconnect(cleanup))
	}()
	ping, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = client.Ping(ping, nil)
	cancel()
	if err != nil {
		return err
	}
	authenticate := authentication.HandlerFunc(func(_ context.Context, request *http.Request) (authentication.Result, error) {
		credential := request.Header.Get("Authorization")
		if credential == "" {
			return authentication.Anonymous(), nil
		}
		if subtle.ConstantTimeCompare([]byte(credential), []byte("Bearer "+secret)) != 1 {
			return authentication.Failed("invalid local-demo credential"), nil
		}
		return authentication.Authenticated(identity.NewPrincipal(identity.PrincipalData{ID: "reader", AuthenticationType: "local-demo-secret"}))
	})
	resolver, err := tenancy.NewResolver(tenancy.Options{Strategy: tenancy.Fixed, FixedID: tenant})
	if err != nil {
		return err
	}
	membership := tenancy.MembershipFunc(func(_ context.Context, principal identity.Principal, selected tenancy.ID) (bool, error) {
		return principal.ID() == "reader" && selected == tenant, nil
	})
	app, err := snapshot.SnapshotHTTPExample(client, []authentication.Handler{authenticate}, resolver, membership)
	if err != nil {
		return err
	}
	// Run joins Arc requests before returning; only then does the client defer
	// disconnect the application-owned driver client.
	return app.Run(ctx, "127.0.0.1:8080")
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Getenv("ARC_MONGODB_URI"), os.Getenv("ARC_EXAMPLE_TOKEN"), os.Getenv("ARC_EXAMPLE_TENANT")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
