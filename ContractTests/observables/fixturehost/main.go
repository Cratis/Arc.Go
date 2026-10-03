// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// fixturehost is a loopback-only executable for the real Arc frontend contracts.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/cratis/arc.go/ContractTests/observables/clientfixture"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	fixture, err := clientfixture.New()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	// This announces the address, not readiness. The runner must successfully GET
	// /fixture/ready through Arc admission before executing any client cases.
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"origin": "http://" + listener.Addr().String()}); err != nil {
		return errors.Join(fmt.Errorf("announce listener: %w", err), listener.Close())
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return fixture.App.Serve(ctx, listener)
}
