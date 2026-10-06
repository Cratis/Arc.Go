// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// host is the loopback-only Arc fixture executable for the real-browser lane.
// It serves the bundled browser assets and the production-generated queries on
// one origin with Arc's default cookie-owned anonymous hub sessions.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/cratis/arc.go/ContractTests/internal/observables/clientfixture"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	assets := flag.String("assets", "", "directory holding the bundled browser assets")
	flag.Parse()
	if *assets == "" || flag.NArg() != 0 {
		return fmt.Errorf("usage: host -assets DIR")
	}
	info, err := os.Stat(*assets)
	if err != nil || !info.IsDir() {
		return errors.Join(fmt.Errorf("asset directory %q is unavailable", *assets), err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	out := json.NewEncoder(os.Stdout)
	// This announces the address, not readiness. The runner must successfully GET
	// /fixture/ready through Arc admission before opening any page.
	if err := out.Encode(map[string]string{"origin": "http://" + listener.Addr().String()}); err != nil {
		return errors.Join(fmt.Errorf("announce listener: %w", err), listener.Close())
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return clientfixture.ServeBrowser(ctx, listener, os.DirFS(*assets), func(report clientfixture.BrowserReport) error {
		return out.Encode(report)
	})
}
