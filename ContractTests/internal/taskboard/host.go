// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package taskboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
)

// Run starts the real Arc fixture host on an ephemeral IPv4 loopback port. It
// writes exactly one readiness JSON line to ready when Serve first accepts, after
// application startup. Cancellation stops and joins the server. Errors, including
// readiness publication failure, are returned to the executable/test owner.
func Run(ctx context.Context, ready io.Writer) error {
	app, err := New()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	return app.Serve(ctx, &readyListener{Listener: listener, ready: ready})
}

type readyListener struct {
	net.Listener
	ready io.Writer
	once  sync.Once
	err   error
}

func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() {
		l.err = json.NewEncoder(l.ready).Encode(struct {
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
