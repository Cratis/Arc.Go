// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { join } from 'node:path';
import test from 'node:test';
import { build } from 'esbuild';
import { DateOnly, Fields, Guid, JsonSerializer, TimeOnly, TimeSpan } from '@cratis/fundamentals';

// Run modes in separate Node processes, never sharing derived registries with
// one another or the unchanged pinned C# reference fixtures.
const mode = process.env.ARC_MODEL_DECORATORS ?? 'standard';
assert.ok(['standard', 'legacy'].includes(mode));
const bundle = await build({
    entryPoints: [join(import.meta.dirname, 'Generated/Shop/Models/index.ts')],
    tsconfig: join(import.meta.dirname, mode === 'legacy' ? 'tsconfig.legacy.json' : 'tsconfig.json'),
    bundle: true, write: false, keepNames: true, platform: 'node', format: 'esm', target: 'es2022',
    plugins: [{ name: 'locked-runtime', setup(builder) {
        builder.onResolve({ filter: /^@cratis\// }, args => ({ path: import.meta.resolve(args.path), external: true }));
    } }]
});
const { Access, allAccess, Detail, Notice, Scalars, State, UrgentNotice } = await import(
    'data:text/javascript;base64,' + Buffer.from(bundle.outputFiles[0].text).toString('base64'));

const id = '12345678-90ab-cdef-0123-456789abcdef';
const otherID = 'abcdef12-3456-7890-abcd-ef1234567890';
const derivedID = '1578f20a-cd63-456f-98aa-c97daf05d0fa';
// Independent wire values, not values serialized by the system under test.
function wire() {
    return { id, day: '2026-01-02', startsAt: '03:04:05.123', duration: '-2.03:04:05.1234567',
        created: '2026-01-02T03:04:05.000Z', idConcept: otherID, dayConcept: '1999-12-31',
        timeConcept: '23:59:58.987', spanConcept: '1.00:00:00.0000001', nameConcept: 'plain',
        boolConcept: false, numberConcept: 9007199254740991, state: 7, access: 5, states: [0, 42], counts: { first: 0, next: -3 },
        details: [{ id: otherID, label: 'nested' }], ids: [id, otherID], days: ['2026-01-02'],
        times: ['03:04:05.123'], spans: ['-2.03:04:05.1234567'], tags: ['first', ''], values: [0, -2.5], flags: [false, true],
        description: 'optional', nullableId: otherID, nullableDetail: { id, label: 'optional nested' },
        optionalCount: 0, 'EXACT-name': 'not recased', URL: 'acronym',
        notice: { _derivedTypeId: derivedID, title: 'urgent', priority: 7 } };
}
function hydrate(value) { return JsonSerializer.deserialize(Scalars, JSON.stringify(value)); }

test(`${mode}: property metadata uses the exact C# constructor/enumerable shapes and wire names`, () => {
    const fields = Fields.getFieldsForType(Scalars);
    assert.deepEqual(fields.map(({ name, type, enumerable }) => [name, type, enumerable]), [
        ['id', Guid, false], ['day', DateOnly, false], ['startsAt', TimeOnly, false], ['duration', TimeSpan, false], ['created', Date, false],
        ['idConcept', Guid, false], ['dayConcept', DateOnly, false], ['timeConcept', TimeOnly, false], ['spanConcept', TimeSpan, false],
        ['nameConcept', String, false], ['boolConcept', Boolean, false], ['numberConcept', Number, false],
        ['state', Number, false], ['access', Number, false], ['states', Number, true], ['counts', Object, false], ['details', Detail, true], ['ids', Guid, true],
        ['days', DateOnly, true], ['times', TimeOnly, true], ['spans', TimeSpan, true], ['tags', String, true],
        ['values', Number, true], ['flags', Boolean, true], ['description', String, false], ['nullableId', Guid, false],
        ['nullableDetail', Detail, false], ['optionalCount', Number, false], ['EXACT-name', String, false], ['URL', String, false], ['notice', Notice, false]
    ]);
    assert.deepEqual(Fields.getFieldsForType(UrgentNotice).map(field => field.name), ['title', 'priority']);
    assert.equal(State.draft, 0);
    assert.equal(State.published, 7);
    assert.equal(State.alias, 7);
    assert.equal(State.archived, -2);
    assert.equal(State.default, 42);
    assert.equal(Access.read, 1);
    assert.equal(Access.write, 4);
    assert.equal(allAccess, 5);
});

test(`${mode}: real Fundamentals hydration and round-trip reconstruct scalars, concepts, arrays and derived classes`, () => {
    const value = wire();
    const model = hydrate(value);
    assert.ok(model instanceof Scalars);
    for (const field of ['id', 'idConcept', 'nullableId']) assert.ok(model[field] instanceof Guid);
    for (const field of ['day', 'dayConcept']) assert.ok(model[field] instanceof DateOnly);
    for (const field of ['startsAt', 'timeConcept']) assert.ok(model[field] instanceof TimeOnly);
    for (const field of ['duration', 'spanConcept']) assert.ok(model[field] instanceof TimeSpan);
    assert.ok(model.created instanceof Date);
    assert.ok(model.details[0] instanceof Detail);
    assert.ok(model.details[0].id instanceof Guid);
    assert.ok(model.nullableDetail instanceof Detail);
    assert.ok(model.notice instanceof Notice);
    assert.ok(model.notice instanceof UrgentNotice);
    assert.equal(model.notice.priority, 7);
    for (const [field, Type] of [['ids', Guid], ['days', DateOnly], ['times', TimeOnly], ['spans', TimeSpan]]) {
        assert.ok(model[field][0] instanceof Type);
    }
    assert.equal(model.boolConcept, false);
    assert.equal(model.numberConcept, 9007199254740991);
    assert.equal(model['EXACT-name'], value['EXACT-name']);
    assert.equal(model.URL, value.URL);
    assert.deepEqual(JSON.parse(JsonSerializer.serialize(model)), value);
    const base = hydrate({ ...value, notice: { title: 'ordinary' } });
    assert.ok(base.notice instanceof Notice);
    assert.equal(base.notice instanceof UrgentNotice, false);
    assert.deepEqual(JSON.parse(JsonSerializer.serialize(base)), { ...value, notice: { title: 'ordinary' } });
});

test(`${mode}: missing and empty collections and optional fields retain pinned runtime semantics`, () => {
    const value = wire();
    for (const name of ['description', 'nullableId', 'nullableDetail', 'optionalCount']) delete value[name];
    const model = hydrate(value);
    assert.equal(model.description, undefined);
    assert.equal(model.nullableId, undefined);
    assert.equal(model.nullableDetail, undefined);
    assert.equal(model.optionalCount, undefined);
    assert.deepEqual(JSON.parse(JsonSerializer.serialize(model)), value);
    const absent = hydrate({});
    for (const name of ['states', 'details', 'ids', 'days', 'times', 'spans', 'tags', 'values', 'flags']) assert.deepEqual(absent[name], []);
    assert.deepEqual(hydrate({ tags: [], details: [] }).tags, []);
});

test(`${mode}: explicit null hydration is preserved; pinned serializer null writes remain an upstream limitation`, () => {
    const model = hydrate({ description: null, nullableId: null, nullableDetail: null, tags: null });
    for (const name of ['description', 'nullableId', 'nullableDetail', 'tags']) assert.equal(model[name], null);
    // Do not patch the runtime or silently replace its serializer to fake parity.
    assert.throws(() => JsonSerializer.serialize(model), TypeError);
});

test(`${mode}: temporal conversion and numeric precision limits remain visible`, () => {
    const value = wire();
    value.startsAt = '03:04:05.1234567';
    value.created = '2026-01-02T03:04:05.1234567+02:00';
    const model = hydrate(value);
    assert.deepEqual([model.day.year, model.day.month, model.day.day], [2026, 1, 2]);
    assert.equal(model.day.toString(), '2026-01-02');
    assert.equal(model.startsAt.toString(), '03:04:05.123');
    assert.equal(model.created.toISOString(), '2026-01-02T01:04:05.123Z');
    assert.equal(model.duration.ticks, -1838451234567);
    assert.equal(model.duration.toString(), '-2.03:04:05.1234567');
    assert.equal(model.spanConcept.ticks, 864000000001);
    assert.equal(model.numberConcept + 1, model.numberConcept + 2);
});
