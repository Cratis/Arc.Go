// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

assert.equal(process.version, 'v26.8.1');
// Read-only reuse of the existing locked ProxyComparison installation is allowed.
// No dependency installation or writes occur through this optional external path.
export const modules = resolve(process.env.ARC_ENUM_NODE_MODULES ?? resolve(import.meta.dirname, '../../ProxyComparison/node_modules'));
export const require = createRequire(resolve(modules, '../package.json'));
export const readJSON = file => JSON.parse(readFileSync(file, 'utf8'));
const lock = readJSON(resolve(import.meta.dirname, '../../ProxyComparison/package-lock.json'));
for (const [name, version] of Object.entries({ '@cratis/fundamentals': '7.22.0', esbuild: '0.25.10', typescript: '5.9.3' })) {
    assert.equal(readJSON(resolve(modules, name, 'package.json')).version, version);
    assert.equal(lock.packages['node_modules/' + name].version, version);
    assert.ok(lock.packages['node_modules/' + name].integrity);
}
export const fundamentalsURL = pathToFileURL(resolve(modules, '@cratis/fundamentals/dist/esm/index.js')).href;
