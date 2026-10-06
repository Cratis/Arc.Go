// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { registerHooks } from 'node:module';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { ObservableQueryFor, QueryFor, QueryHttpMethod, SortDirection } from '@cratis/arc/queries';

const mode = process.env.ARC_OBSERVABLE_DECORATORS ?? 'standard';
assert.ok(['standard', 'legacy'].includes(mode));
const locked = JSON.parse(readFileSync(new URL('../package-lock.json', import.meta.url)));
for (const [name, version] of [['@cratis/arc','22.48.2'], ['@cratis/arc.react','22.48.2'], ['@cratis/fundamentals','7.22.0'], ['react','18.3.1']]) {
    assert.equal(locked.packages[`node_modules/${name}`].version, version);
    assert.equal(JSON.parse(readFileSync(new URL(`../node_modules/${name}/package.json`, import.meta.url))).version, version);
}
const modules = new Map(['@cratis/fundamentals', '@cratis/arc/queries', '@cratis/arc/commands', '@cratis/arc/reflection', '@cratis/arc.react/queries', '@cratis/arc.react/commands']
    .map(specifier => [specifier, import.meta.resolve(specifier)]));
registerHooks({ resolve(specifier, context, nextResolve) {
    if (modules.has(specifier)) return nextResolve(modules.get(specifier), context);
    if (specifier.startsWith('.') && context.parentURL?.includes('/tsc-')) {
        const candidate = new URL(specifier + '.js', context.parentURL);
        if (existsSync(fileURLToPath(candidate))) return nextResolve(candidate.href, context);
    }
    return nextResolve(specifier, context);
} });
const { Watch, Single, Nullable, Private, Alias, Page, Changes, Options, Rename, All, Task } = await import(
    `../../../.ai-work/output/ts-observable/tsc-${mode}/Generated/Shop/Tasks/index.js`);
const envelope = data => ({ data, isSuccess:true, isReady:true, isAuthorized:true, isValid:true, hasExceptions:false,
    validationResults:[], exceptionMessages:[], exceptionStackTrace:'', paging:{page:0,size:0,totalItems:0,totalPages:0} });

// Controlled fetch is client-unit evidence only. Subscription transport, real
// hooks and generated server round-trips belong to the next bounded fixture.
test(`${mode}: production-generated observable metadata and runtime inheritance`, () => {
    const query = new Watch();
    assert.ok(query instanceof ObservableQueryFor);
    assert.ok(new All() instanceof QueryFor);
    assert.equal(query.route, '/api/shop/tasks/watch');
    assert.equal(query.queryName, 'Shop.Tasks.Task.Watch');
    assert.equal(query.modelType, Task); assert.equal(query.enumerable, true);
    assert.deepEqual(query.defaultValue, []); assert.deepEqual(query.requiredRequestParameters, ['board']);
    assert.deepEqual(query.parameterDescriptors.map(value => [value.name,value.type,value.isEnumerable]), [['board',String,false]]);
    assert.equal(query.board, undefined); assert.deepEqual(new Private().roles, ['Reader']);
    const options = new Options(); assert.equal(options.count,undefined); assert.equal(options.enabled,undefined);
    assert.deepEqual(options.requiredRequestParameters,[]); assert.equal(typeof new Rename().execute,'function');
    assert.equal(new Single().enumerable,false); assert.equal(new Nullable().enumerable,false);
    for (const Type of [Alias, Page, Changes]) {
        assert.equal(new Type().modelType,Task); assert.equal(new Type().enumerable,true);
        assert.deepEqual(new Type().defaultValue,[]);
        assert.equal(typeof Type.useChangeStream,'function'); assert.equal(typeof Type.useWithPaging,'function');
    }
    assert.equal(typeof query.subscribe,'function'); assert.equal(typeof query.dispose,'function');
    const sorting = Watch.sortBy.title.ascending;
    assert.equal(sorting.field,'title'); assert.equal(sorting.direction,SortDirection.ascending);
    query.sortBy.title.descending(); assert.equal(query.sorting.direction,SortDirection.descending);
    assert.equal(query.paging.hasPaging,false);
    query.dispose();
});

test(`${mode}: generated observable delegates inherited snapshot perform and hydration to locked client`, async t => {
    const original = globalThis.fetch; t.after(() => {globalThis.fetch = original;});
    let calls = 0, recorded;
    globalThis.fetch = async (url, options) => {calls++; recorded={url:new URL(url),options}; return Response.json(envelope([{id:'task-1',title:'first'}]));};
    const query = new Watch(); query.setOrigin('https://observable.invalid'); query.setApiBasePath('/tenant');
    query.setHttpMethod(QueryHttpMethod.Get); query.setHttpHeadersCallback(() => ({'X-Unit':'observable'}));
    assert.equal((await query.perform()).isSuccess,false); assert.equal(calls,0);
    const result = await query.perform({board:'a & b'});
    assert.equal(calls,1); assert.equal(recorded.url.pathname,'/tenant/api/shop/tasks/watch');
    assert.equal(recorded.url.searchParams.get('board'),'a & b'); assert.equal(recorded.options.method,'GET');
    assert.equal(new Headers(recorded.options.headers).get('X-Unit'),'observable');
    assert.equal(result.isSuccess,true); assert.ok(result.data[0] instanceof Task);
    assert.equal(result.data[0].id,'task-1'); assert.equal(result.data[0].title,'first');
    query.dispose();
});
