// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { registerHooks } from 'node:module';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { Fields, Guid, JsonSerializer } from '@cratis/fundamentals';

// Execute untouched tsc-produced JS, not esbuild's hygienically renamed bundle.
// The extension resolver handles only TS's extensionless relative imports; it
// does not transform code or substitute package declarations/runtime modules.
const lockedModules = new Map(['@cratis/fundamentals'].map(specifier => [specifier, import.meta.resolve(specifier)]));
registerHooks({ resolve(specifier, context, nextResolve) {
    if (lockedModules.has(specifier)) return nextResolve(lockedModules.get(specifier), context);
    if (specifier.startsWith('.') && context.parentURL?.includes('/tsc-')) {
        const candidate = new URL(specifier + '.js', context.parentURL);
        if (existsSync(fileURLToPath(candidate))) return nextResolve(candidate.href, context);
    }
    return nextResolve(specifier, context);
} });
const models = await import('../../../.ai-work/output/ts-command/tsc-models/Generated/Shop/Models/index.js');
const { Reference } = await import('../../../.ai-work/output/ts-command/tsc-symbol/Reference.js');
const { Symbol: ExternalSymbol } = await import('../../../.ai-work/output/ts-command/tsc-symbol/Symbol.js');

test('actual tsc reference/import alias leaves global Symbol.metadata available', () => {
    assert.equal(Fields.getFieldsForType(Reference)[0].type, ExternalSymbol);
    assert.equal(Fields.getFieldsForType(Reference)[0].name, 'value');
    assert.equal(typeof Symbol.metadata, 'symbol');
});

test('actual tsc standard decorators retain metadata in exported and referenced models', () => {
    const { Scalars, Detail } = models;
    const id = '12345678-90ab-cdef-0123-456789abcdef';
    assert.equal(Fields.getFieldsForType(Scalars).find(field => field.name === 'details').type, Detail);
    assert.equal(Fields.getFieldsForType(Detail).find(field => field.name === 'id').type, Guid);
    const value = JsonSerializer.deserialize(Scalars, JSON.stringify({ details: [{ id, label: 'referenced' }] }));
    assert.ok(value instanceof Scalars);
    assert.ok(value.details[0] instanceof Detail);
    assert.ok(value.details[0].id instanceof Guid);
    assert.equal(value.details[0].id.toString(), id);
});
