// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { registerHooks } from 'node:module';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { Guid, DateOnly, TimeOnly, TimeSpan, Fields } from '@cratis/fundamentals';
import { Globals } from '@cratis/arc';
import { Paging, QueryHttpMethod, SortDirection } from '@cratis/arc/queries';

const mode = process.env.ARC_QUERY_DECORATORS ?? 'standard';
assert.ok(['standard', 'legacy'].includes(mode));
const locked = JSON.parse(readFileSync(new URL('../package-lock.json', import.meta.url)));
for (const [name, version] of [['@cratis/arc','22.48.2'], ['@cratis/arc.react','22.48.2'], ['@cratis/fundamentals','7.22.0'], ['react','18.3.1']]) {
    assert.equal(locked.packages[`node_modules/${name}`].version, version);
    assert.equal(JSON.parse(readFileSync(new URL(`../node_modules/${name}/package.json`, import.meta.url))).version, version);
}
const modules = new Map(['@cratis/fundamentals', '@cratis/arc/queries', '@cratis/arc/reflection', '@cratis/arc.react/queries']
    .map(specifier => [specifier, import.meta.resolve(specifier)]));
registerHooks({ resolve(specifier, context, nextResolve) {
    if (modules.has(specifier)) return nextResolve(modules.get(specifier), context);
    if (specifier.startsWith('.') && context.parentURL?.includes('/tsc-')) {
        const candidate = new URL(specifier + '.js', context.parentURL);
        if (existsSync(fileURLToPath(candidate))) return nextResolve(candidate.href, context);
    }
    return nextResolve(specifier, context);
} });
const { All, Array: ArrayQuery, Find, Paged, GetOnly, QueryOnly, Listing, Detail, Status, Tasks } = await import(
    `../../../.ai-work/output/ts-query/tsc-${mode}/Generated/Shop/Queries/index.js`);
const id = '12345678-90ab-cdef-0123-456789abcdef';
// Controlled fetch envelopes below are client-unit evidence, NOT backend evidence.
const envelope = (data, paging = {page:0,size:0,totalItems:0,totalPages:0}) => ({
    data, isSuccess:true, isReady:true, isAuthorized:true, isValid:true, hasExceptions:false,
    validationResults:[], exceptionMessages:[], exceptionStackTrace:'', paging
});
const wire = () => ({ id, name:'concept scalar', enabled:false, status:3, detail:{id,label:'nested concept'},
    created:'2024-05-06T12:30:40+02:00', day:'2024-05-06', clock:'12:30:40.125', duration:'-1.02:03:04.005', tags:[] });
function mockFetch(t, callback) {
    const original = globalThis.fetch; globalThis.fetch = callback;
    t.after(() => {globalThis.fetch = original;});
}
function checkEnvelope(result, expected) {
    for (const name of ['isSuccess','isReady','isAuthorized','isValid','hasExceptions','exceptionMessages','exceptionStackTrace']) {
        assert.deepEqual(result[name],expected[name],name);
    }
    assert.deepEqual(result.validationResults.map(value => ({severity:value.severity,message:value.message,members:value.members,state:value.state})),expected.validationResults);
    assert.deepEqual({page:result.paging.page,size:result.paging.size,totalItems:result.paging.totalItems,totalPages:result.paging.totalPages},expected.paging);
}
function checkListing(value) {
    assert.ok(value instanceof Listing); assert.ok(value.detail instanceof Detail);
    assert.ok(value.id instanceof Guid); assert.equal(value.id.toString(),id);
    assert.ok(value.detail.id instanceof Guid); assert.equal(value.detail.label,'nested concept');
    assert.equal(value.name,'concept scalar'); assert.equal(value.enabled,false); assert.equal(value.status,Status.done);
    assert.ok(value.created instanceof Date); assert.equal(value.created.toISOString(),'2024-05-06T10:30:40.000Z');
    assert.ok(value.day instanceof DateOnly); assert.equal(value.day.toString(),'2024-05-06');
    assert.ok(value.clock instanceof TimeOnly); assert.ok(value.duration instanceof TimeSpan);
    assert.deepEqual(value.tags,[]); assert.equal(value.description,undefined);
}

test(`${mode}: model-owned snapshot metadata, undefined defaults, exact requiredness and plain parameter assignment`, () => {
    const query = new Find();
    assert.equal(query.queryName,'Shop.Queries.Listing.Find');
    assert.equal(query.route,'/api/shop/queries/find');
    assert.equal(query.modelType,Listing); assert.equal(query.enumerable,false);
    assert.deepEqual(query.defaultValue,{}); assert.deepEqual(query.requiredRequestParameters,['id']);
    assert.deepEqual(query.roles,['Editor','Admin','Reader']);
    assert.deepEqual(query.parameterDescriptors.map(value => [value.name,value.type,value.isEnumerable]),[
        ['id',Guid,false],['count',Number,false],['enabled',Boolean,false],['EXACT-name',String,false],['tags',String,true]
    ]);
    for (const parameter of query.parameterDescriptors) assert.equal(query[parameter.name],undefined);
    // Snapshot QueryFor has no propertyChanged/onPropertyChanged contract. Its
    // descriptors collect plain instance fields; setters must not invent one.
    assert.equal(query.propertyChanged,undefined);
    query.enabled = false; query.count = 0;
    assert.equal(query.enabled,false); assert.equal(query.count,0);
    assert.equal(Fields.getFieldsForType(Listing).find(field => field.name === 'detail').type,Detail);
    assert.equal(Fields.getFieldsForType(Listing).find(field => field.name === 'name').type,String);
    assert.equal(typeof Find.useSuspense,'function'); assert.equal(typeof All.useWithPaging,'function');
});

test(`${mode}: client-unit required argument guards network; property assignment does not bypass args requiredness`, async t => {
    let calls = 0; mockFetch(t,async () => {calls++; return Response.json(envelope(wire()));});
    const query = new Find(); query.setOrigin('https://example.invalid'); query.id = Guid.parse(id);
    assert.equal((await query.perform()).isSuccess,false);
    assert.equal(calls,0);
    query.parameters = {id:Guid.parse(id)};
    const result = await query.perform(); assert.equal(calls,1); checkListing(result.data);
});

test(`${mode}: client-unit GET carries args, supplied zero/false/empty lists, exact wire fields and client options`, async t => {
    const query = new Find(); query.setOrigin('https://example.invalid'); query.setApiBasePath('/tenant');
    query.setMicroservice('taskboard'); query.setHttpHeadersCallback(() => ({'X-Unit':'query'}));
    const expected = envelope(wire()); let recorded;
    mockFetch(t,async (url,options) => {recorded = {url:new URL(url),options}; return Response.json(expected);});
    const result = await query.perform({id:Guid.parse(id),count:0,enabled:false,tags:[], 'EXACT-name':'a & b'});
    assert.equal(recorded.options.method,'GET'); assert.equal(recorded.options.body,undefined);
    assert.equal(recorded.url.pathname,'/tenant/api/shop/queries/find');
    assert.equal(recorded.url.searchParams.get('id'),id); assert.equal(recorded.url.searchParams.get('count'),'0');
    assert.equal(recorded.url.searchParams.get('enabled'),'false'); assert.equal(recorded.url.searchParams.get('EXACT-name'),'a & b');
    const headers = new Headers(recorded.options.headers);
    assert.equal(headers.get('Accept'),'application/json'); assert.equal(headers.get('Content-Type'),'application/json');
    assert.equal(headers.get('X-Unit'),'query'); assert.equal(headers.get(Globals.microserviceHttpHeader),'taskboard');
    assert.ok(recorded.options.signal instanceof AbortSignal);
    checkEnvelope(result,expected); checkListing(result.data);
    await query.perform({id:Guid.parse(id)});
    assert.equal(recorded.url.searchParams.has('enabled'),false); assert.equal(recorded.url.searchParams.has('EXACT-name'),false);
    query.enabled = false; query.count = 0; query['EXACT-name'] = 'instance';
    await query.perform({id:Guid.parse(id),count:99,'EXACT-name':'argument'});
    assert.equal(recorded.url.searchParams.get('count'),'0'); assert.equal(recorded.url.searchParams.get('EXACT-name'),'instance');
});

test(`${mode}: client-unit paged QUERY data is a model array and paging lives only on the complete envelope`, async t => {
    const query = new Paged(); query.setOrigin('https://paged.invalid');
    query.paging = new Paging(2,5); query.sortBy.name.descending();
    const paging = {page:2,size:5,totalItems:12,totalPages:3}; const expected = envelope([wire()],paging);
    let request; mockFetch(t,async (url,options) => {request={url:new URL(url),options}; return Response.json(expected);});
    const result = await query.perform({id:Guid.parse(id),enabled:false,count:0,tags:[]});
    assert.equal(request.options.method,'QUERY'); assert.equal(request.url.search,'');
    assert.deepEqual(JSON.parse(request.options.body),{arguments:{id,enabled:false,count:0,tags:[]},paging:{page:2,pageSize:5},sorting:{field:'name',direction:'desc'}});
    assert.equal(query.queryName,'Shop.Queries.Listing.Paged'); assert.equal(query.modelType,Listing); assert.equal(query.enumerable,true);
    checkEnvelope(result,expected); assert.ok(Array.isArray(result.data)); checkListing(result.data[0]);
    assert.equal(result.data.items,undefined); assert.equal(result.data.totalItems,undefined);
});

test(`${mode}: client-unit declared result sorting allowlist is deterministic; plain lists remain unpaged`, async t => {
    const query = new All(); query.setOrigin('https://lists.invalid');
    assert.equal(query.paging.hasPaging,false); assert.equal(new Tasks().paging.hasPaging,false);
    assert.deepEqual(query.defaultValue,[]); assert.deepEqual(query.parameterDescriptors,[]);
    assert.deepEqual(Object.getOwnPropertyNames(Object.getPrototypeOf(query.sortBy)),['constructor','enabled','name','status']);
    assert.equal(query.sortBy.id,undefined); assert.equal(query.sortBy.detail,undefined);
    assert.equal(query.sortBy.name,query.sortBy.name); assert.equal(All.sortBy.name,All.sortBy.name);
    const original = query.sorting;
    const sorting = All.sortBy.name.ascending;
    assert.equal(sorting.field,'name'); assert.equal(sorting.direction,SortDirection.ascending); assert.equal(query.sorting,original);
    const changed = query.sortBy.status.descending(); assert.equal(query.sorting,changed);
    query.setHttpMethod(QueryHttpMethod.Get); query.paging = new Paging(1,4);
    let recorded; mockFetch(t,async (url,options) => {recorded={url:new URL(url),options}; return Response.json(envelope([]));});
    assert.deepEqual((await query.perform()).data,[]);
    assert.equal(recorded.url.searchParams.get('sortBy'),'status'); assert.equal(recorded.url.searchParams.get('sortDirection'),'desc');
    assert.equal(recorded.url.searchParams.get('page'),'1'); assert.equal(recorded.url.searchParams.get('pageSize'),'4');
});

test(`${mode}: client-unit finalized explicit Get, QUERY-only inference, Auto fallback and unspecified Globals preference`, async t => {
    const original = Globals.queryHttpMethod;
    t.after(() => {Globals.queryHttpMethod = original;});
    Globals.queryHttpMethod = QueryHttpMethod.Query;
    const calls = [];
    mockFetch(t,async (url,options) => {calls.push({url:String(url),method:options.method});
        if (String(url).includes('auto.invalid') && options.method === 'QUERY') return new Response('',{status:405});
        return Response.json(envelope([]));
    });
    const get = new GetOnly(); get.setOrigin('https://explicit.invalid'); await get.perform();
    const only = new QueryOnly(); only.setOrigin('https://only.invalid'); await only.perform();
    const unspecified = new All(); unspecified.setOrigin('https://globals.invalid'); await unspecified.perform();
    const auto = new ArrayQuery(); auto.setOrigin('https://auto.invalid'); await auto.perform();
    assert.deepEqual(calls.map(value => value.method),['GET','QUERY','QUERY','QUERY','GET']);
    assert.equal(get._httpMethod,QueryHttpMethod.Get); assert.equal(only._httpMethod,QueryHttpMethod.Query);
    assert.equal(unspecified._httpMethod,undefined); assert.equal(auto._httpMethod,QueryHttpMethod.Auto);
});

test(`${mode}: client-unit denial envelopes, null/missing/empty results and snapshot collection hydration`, async t => {
    let expected = envelope([wire()]); mockFetch(t,async () => Response.json(expected));
    const query = new All(); query.setOrigin('https://shapes.invalid');
    checkListing((await query.perform()).data[0]);
    expected = envelope([]); assert.deepEqual((await query.perform()).data,[]);
    expected = envelope(null); assert.deepEqual((await query.perform()).data,[]);
    expected = envelope(undefined); assert.deepEqual((await query.perform()).data,[]);
    expected = {...envelope(null),isSuccess:false,isAuthorized:false};
    const denied = await query.perform(); checkEnvelope(denied,expected); assert.deepEqual(denied.data,[]);
    const single = new Find(); single.setOrigin('https://shapes.invalid');
    assert.equal((await single.perform({id:Guid.parse(id)})).data,null);
});
