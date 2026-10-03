# TypeScript Symbol binding regression

`Symbol` is not a supported generated model export. The renderer rejects it with
`invalid TypeScript export name "Symbol"`, including when another model refers
to it. Rename the logical export (for example `DomainSymbol`, using the existing
model name override). A filename suffix alone does not rename its binding and
cannot fix the collision. Import planning also reserves the global `Symbol`.

TypeScript 5.9.3's standard-decorator JS uses global `Symbol.metadata`. A local
`Symbol` class/import hides it; successful compilation and esbuild's hygienic
renaming do not prove that emitted JS loads. `typescript_symbol_test.go` covers
the rejected model/reference and safe import alias. The runtime regression loads
actual tsc-produced model JS, with an extensionless-import resolver and exact locked-package mapping:

```sh
./node_modules/.bin/tsc -p Models/tsconfig.json --noEmit false --outDir ../../.ai-work/output/ts-command/tsc-models
./node_modules/.bin/tsc -p Commands/tsconfig.symbol.json --noEmit false --outDir ../../.ai-work/output/ts-command/tsc-symbol
node --test --test-timeout=30000 Commands/tsc-models.test.mjs
```

It verifies standard-decorator Fields and nested model/Guid hydration without
transpiling, bundling, or replacing the locked Fundamentals runtime. A small
hand-authored external Symbol/Reference fixture also executes the exact safe
`Symbol_2` import binding asserted by the Go import-planning test. This is a
binding regression, not permission to generate a public model named Symbol.
