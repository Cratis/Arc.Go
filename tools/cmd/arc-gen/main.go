// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

// Command arc-gen generates typed model-bound Arc registrations for selected packages.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/cratis/arc.go/tools/internal/artifacts"
)

func main() {
	var config artifacts.Config
	flag.StringVar(&config.Dir, "dir", "", "module directory (default: current directory)")
	flag.StringVar(&config.Tags, "tags", "", "comma-separated Go build tags")
	flag.BoolVar(&config.Check, "check", false, "verify generated output without writing")
	flag.Parse()
	config.Patterns = flag.Args()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := artifacts.Generate(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
