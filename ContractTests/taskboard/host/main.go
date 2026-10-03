// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Command host runs the real-pipeline task-board conformance fixture on loopback.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/cratis/arc.go/ContractTests/taskboard"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app, err := taskboard.New()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	return app.Serve(ctx, &readyListener{Listener: listener})
}

type readyListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() {
		l.err = json.NewEncoder(os.Stdout).Encode(struct {
			Kind    string `json:"kind"`
			BaseURL string `json:"baseUrl"`
		}{"arc-go-conformance-ready", "http://" + l.Addr().String()})
		if l.err != nil {
			l.err = errors.Join(l.err, l.Close())
		}
	})
	if l.err != nil {
		return nil, l.err
	}
	return l.Listener.Accept()
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
