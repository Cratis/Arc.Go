// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import { resolve } from 'node:path';
import { modules, require } from './dependencies.mjs';

const ts = require('typescript');
const options = {
    target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler, strict: true, noEmit: true,
    skipLibCheck: false, types: [],
    paths: { '@cratis/fundamentals': [resolve(modules, '@cratis/fundamentals/dist/esm/index.d.ts')] }
};
const program = ts.createProgram([resolve(import.meta.dirname, 'model.ts')], options);
const diagnostics = ts.getPreEmitDiagnostics(program);
if (diagnostics.length) {
    console.error(ts.formatDiagnosticsWithColorAndContext(diagnostics, {
        getCanonicalFileName: name => name, getCurrentDirectory: () => process.cwd(), getNewLine: () => '\n'
    }));
    process.exitCode = 1;
} else {
    console.log('TypeScript 5.9.3: strict enum fixture compilation passed');
}
