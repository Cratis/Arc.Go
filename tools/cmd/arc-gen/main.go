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
	config := artifacts.Config{Report: os.Stdout}
	flag.StringVar(&config.Dir, "dir", "", "module directory (default: current directory)")
	flag.StringVar(&config.Tags, "tags", "", "comma-separated Go build tags")
	flag.StringVar(&config.ConfigFile, "config", "", "versioned application profile configuration")
	flag.StringVar(&config.BindingsConfigFile, "bindings-config", "", "separate strict versioned constructor-service configuration")
	flag.BoolVar(&config.Check, "check", false, "verify generated output without writing")
	flag.StringVar(&config.TypeScriptOut, "typescript-out", "", "enable supported TypeScript models, commands, snapshot and observable queries at this output root")
	flag.StringVar(&config.OpenAPIOut, "openapi-out", "", "write the OpenAPI 3.1 document to this module-relative .json file (requires the profile's openapi section)")
	flag.StringVar(&config.ScreenplayOut, "screenplay-out", "", "write partial Screenplay metadata to this module-relative .play file (requires profile formatVersion 2)")
	emitGo := flag.Bool("emit-go", true, "emit Go adapters (false is currently unsupported with TypeScript output)")
	flag.Parse()
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "emit-go" {
			config.EmitGo = emitGo
		}
	})
	config.Patterns = flag.Args()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := artifacts.Generate(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
