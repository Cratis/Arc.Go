// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import test from 'node:test';
import { build } from 'esbuild';
import { Guid, JsonSerializer } from '@cratis/fundamentals';
import { filesIn, stableBytes } from './reference.mjs';

const directory = import.meta.dirname;
const expected = ['All.ts', 'Detail.ts', 'Listing.ts', 'Notice.ts', 'Observe.ts', 'Register.ts', 'Status.ts', 'UrgentNotice.ts', 'index.ts'].map(name => `ProxyComparison/${name}`);

// Bundle relative imports only, retaining exactly one real pinned Fundamentals
// runtime and its derived registry. No substitute declarations or serializers.
const output = await build({ entryPoints: [join(directory, 'Snapshots/Primary22.48.2/ProxyComparison/index.ts')], bundle: true,
    write: false, keepNames: true, platform: 'node', format: 'esm', target: 'es2022', plugins: [{ name: 'pinned-runtime', setup(builder) {
        builder.onResolve({ filter: /^(@cratis\/|react(?:\/|$))/ }, args => ({ path: import.meta.resolve(args.path), external: true }));
    } }] });
const proxies = await import('data:text/javascript;base64,' + Buffer.from(output.outputFiles[0].text).toString('base64'));

test('primary and historical inventories have valid unchanged C# body hashes', async () => {
    for (const family of ['Primary22.48.2', 'Historical22.45.0']) {
        const snapshots = join(directory, 'Snapshots', family);
        assert.deepEqual(await filesIn(snapshots), expected);
        for (const file of expected) stableBytes(await readFile(join(snapshots, file)));
    }
    const valid = await readFile(join(directory, 'Snapshots/Primary22.48.2/ProxyComparison/Register.ts'));
    assert.throws(() => stableBytes(Buffer.concat([valid, Buffer.from('// edited\n')])), /Invalid C# provenance body hash/);
});

test('C# command validation, setter tracking and required descriptors use the real runtime', () => {
    const { Register, RegisterValidator } = proxies;
    const command = new Register();
    assert.ok(command.validation instanceof RegisterValidator);
    assert.deepEqual(command.propertyDescriptors.map(({ name, type, isOptional }) => [name, type, isOptional]), [
        ['id', Guid, false], ['name', String, false], ['quantity', Number, false]
    ]);
    assert.equal(command.route, '/api/proxy-comparison/register');
    assert.deepEqual(command.requestParameters, []);
    command.setInitialValues({ id: Guid.parse('12345678-90ab-cdef-0123-456789abcdef'), name: 'first', quantity: 1 });
    assert.equal(command.hasChanges, false);
    command.name = 'changed';
    assert.equal(command.hasChanges, true);
    command.revertChanges();
    assert.equal(command.name, 'first');
    assert.equal(command.hasChanges, false);
    const findings = command.validation.validate({ name: '', quantity: 0 }).map(result => result.message).sort();
    assert.deepEqual(findings, ['Name required', 'Quantity must be positive']);
    assert.deepEqual(command.validation.validate({ name: 'x'.repeat(40), quantity: 1 }), []);
    assert.deepEqual(command.validation.validate({ name: 'x'.repeat(41), quantity: 1 }).map(result => result.message), ['Name too long']);
});

test('C# class hydration reconstructs nested models, Guid, Date, enums and derived models', () => {
    const { Listing, Detail, Notice, UrgentNotice, Status } = proxies;
    const wire = { name: 'row', detail: { id: '12345678-90ab-cdef-0123-456789abcdef', created: '2026-01-02T03:04:05.000Z' },
        notice: { _derivedTypeId: '1578f20a-cd63-456f-98aa-c97daf05d0fa', title: 'urgent', priority: 7 }, status: 1 };
    const model = JsonSerializer.deserialize(Listing, JSON.stringify(wire));
    assert.ok(model instanceof Listing);
    assert.ok(model.detail instanceof Detail);
    assert.ok(model.detail.id instanceof Guid);
    assert.ok(model.detail.created instanceof Date);
    assert.ok(model.notice instanceof Notice);
    assert.ok(model.notice instanceof UrgentNotice);
    assert.equal(model.notice.priority, 7);
    assert.equal(model.status, Status.published);
    assert.equal(Status.draft, 0);
    assert.deepEqual(JSON.parse(JsonSerializer.serialize(model)), wire);
});

test('C# argument-based sorting defect is retained as evidence', () => {
    const { All, Observe } = proxies;
    for (const Query of [All, Observe]) {
        const query = new Query();
        assert.deepEqual(query.requiredRequestParameters, ['id']);
        assert.equal(query.sortBy.name, undefined);
        assert.equal(Query.sortBy.name, undefined);
        const sorting = Query.sortBy.id.ascending;
        assert.equal(sorting.field, 'id');
        assert.deepEqual(query.sortBy.id.ascending(), sorting);
        assert.equal(query.queryName, `ProxyComparison.Listing.${Query.name}`);
    }
});
