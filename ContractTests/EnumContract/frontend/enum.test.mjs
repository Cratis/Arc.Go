// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { resolve } from 'node:path';
import test from 'node:test';
import { fundamentalsURL, readJSON, require } from './dependencies.mjs';

const { Fields, JsonSerializer } = await import(fundamentalsURL);
const { build } = require('esbuild');
const bundle = await build({
    entryPoints: [resolve(import.meta.dirname, 'model.ts')],
    bundle: true, write: false, keepNames: true, platform: 'node', format: 'esm', target: 'es2022',
    plugins: [{ name: 'pinned-runtime', setup(builder) {
        builder.onResolve({ filter: /^@cratis\/fundamentals$/ }, () => ({ path: fundamentalsURL, external: true }));
    } }]
});
const { Model, State, Access, allAccess } = await import('data:text/javascript;base64,' + Buffer.from(bundle.outputFiles[0].text).toString('base64'));
const profile = readJSON(resolve(import.meta.dirname, '../profile.json'));
const normalized = readJSON(resolve(import.meta.dirname, '../normalized.json'));
const hydrate = input => JsonSerializer.deserialize(Model, JSON.stringify(input));

// This is the actual Number hydration path, not a simulated enum decoder.
test('metadata and TS-only renames agree with normalized original-name facts', () => {
    assert.deepEqual(Fields.getFieldsForType(Model).map(({ name, type, enumerable }) => [name, type, enumerable]), [
        ['state', Number, false], ['access', Number, false], ['states', Number, true], ['optional', Number, false]
    ]);
    for (const declaration of normalized.enums) {
        const enumObject = declaration.name === 'State' ? State : Access;
        for (const member of declaration.members) assert.equal(enumObject[member.exportName], Number(member.value));
        assert.equal(enumObject.Read, undefined);
        assert.equal(enumObject.reader, 1);
    }
});

test('every admitted C# Int32 result hydrates and serializes without closing the integer domain', () => {
    const values = profile.reads.filter(row => ['State', 'Access'].includes(row.Type) && row.accepted && row.value !== null);
    assert.equal(values.length, 31);
    for (const row of values) {
        assert.equal(row.write.Accepted, true);
        const value = JSON.parse(row.write.Output);
        const property = row.Type === 'State' ? 'state' : 'access';
        const model = hydrate({ [property]: value, states: [value] });
        assert.ok(model instanceof Model);
        assert.equal(model[property], value);
        assert.deepEqual(model.states, [value]);
        const written = JSON.parse(JsonSerializer.serialize(model));
        assert.equal(written[property], value);
        assert.deepEqual(written.states, [value]);
    }
    assert.equal(hydrate({ state: 23 }).state, 23);
    assert.equal(hydrate({ access: 2 }).access, 2);
});

test('missing, null, aliases and flag sign bit remain explicit', () => {
    assert.deepEqual(hydrate({}).states, []);
    assert.equal(hydrate({}).optional, undefined);
    const nullable = hydrate({ optional: null });
    assert.equal(nullable.optional, null);
    assert.throws(() => JsonSerializer.serialize(nullable), TypeError);
    assert.equal(State.writer, State.alias);
    assert.equal(Access.reader | Access.writer, 5);
    assert.equal(allAccess, -1073741819);
    assert.equal(hydrate({ access: -2147483648 }).access, -2147483648);
});

test('backend widths do not inherit frontend safe-number or bitwise domains', () => {
    assert.deepEqual(normalized.outputSchema, { type: 'integer', minimum: -2147483648, maximum: 2147483647 });
    assert.equal(BigInt(normalized.frontend.safeIntegerMaximum), 9007199254740991n);
    assert.equal(BigInt(normalized.frontend.flagsMaximum), 2147483647n);
    assert.equal(Number.isSafeInteger(Number('9007199254740993')), false);
    assert.notEqual(BigInt(Number('9007199254740993')), 9007199254740993n);
    assert.equal(4294967296 | 0, 0);
    assert.equal(2147483648 | 0, -2147483648);
    // Current JS numeric hydration is not server-side enum name validation.
    assert.equal(hydrate({ state: 'Read' }).state, 'Read');
});
