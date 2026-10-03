// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { registerHooks } from 'node:module';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { Guid } from '@cratis/fundamentals';
import { QueryHttpMethod } from '@cratis/arc/queries';

const mode = process.env.ARC_QUERY_DECORATORS ?? 'standard';
assert.ok(['standard','legacy'].includes(mode));
const modules = new Map(['@cratis/fundamentals','@cratis/arc/queries','@cratis/arc/reflection','@cratis/arc.react/queries']
    .map(specifier => [specifier,import.meta.resolve(specifier)]));
registerHooks({resolve(specifier,context,nextResolve) {
    if (modules.has(specifier)) return nextResolve(modules.get(specifier),context);
    if (specifier.startsWith('.') && context.parentURL?.includes('/tsc-')) {
        const candidate = new URL(specifier+'.js',context.parentURL);
        if (existsSync(fileURLToPath(candidate))) return nextResolve(candidate.href,context);
    }
    return nextResolve(specifier,context);
}});
const {Tasks,ByID,Task} = await import(`../../../.ai-work/output/ts-query/tsc-${mode}/Generated/Shop/Queries/index.js`);

test(`${mode}: actual Arc taskboard All/ByID GET and QUERY snapshots, complete independent envelopes`, {timeout:15000}, async () => {
    const child = spawn(fileURLToPath(new URL('../../../.ai-work/output/ts-query/taskboard-host',import.meta.url)),[],{stdio:['ignore','pipe','pipe']});
    let stderr = '';
    child.stderr.on('data',data => {stderr = (stderr+data).slice(-4096);});
    const exited = new Promise(resolve => child.once('exit',(code,signal) => resolve({code,signal})));
    const originalFetch = globalThis.fetch;
    try {
        const origin = await new Promise((resolve,reject) => {
            let output = '';
            const timer = setTimeout(() => reject(new Error('fixture readiness timed out: '+stderr)),5000);
            child.once('error',error => {clearTimeout(timer);reject(error);});
            child.once('exit',code => {clearTimeout(timer);reject(new Error(`fixture exited ${code}: ${stderr}`));});
            child.stdout.on('data',data => {
                output += data;
                if (output.length > 4096) {clearTimeout(timer);reject(new Error('oversized readiness'));return;}
                if (!output.includes('\n')) return;
                clearTimeout(timer);
                try {
                    const ready = JSON.parse(output.split('\n')[0]);
                    assert.equal(ready.kind,'arc-go-conformance-ready');
                    assert.match(ready.baseUrl,/^http:\/\/127\.0\.0\.1:\d+$/);
                    resolve(ready.baseUrl);
                } catch (error) {reject(error);}
            });
        });
        // Real command seeds fixture state; it does not construct query envelopes.
        const createdResponse = await originalFetch(origin+'/api/create-task',{
            method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({title:'snapshot round-trip'}),signal:AbortSignal.timeout(3000)
        });
        assert.equal(createdResponse.status,200);
        const created = await createdResponse.json();
        assert.equal(created.isSuccess,true);
        const id = created.response.id;
        assert.match(id,/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
        assert.notEqual(id,Guid.empty.toString());
        const requests = [];
        globalThis.fetch = async (url,options) => {
            const response = await originalFetch(url,{...options,signal:AbortSignal.timeout(3000)});
            assert.equal(response.status,200);
            assert.equal(response.headers.get('X-Correlation-ID'),'00112233-4455-4677-8899-aabbccddeeff');
            requests.push({url:new URL(url),method:options.method,envelope:await response.clone().json()});
            return response;
        };
        const task = {id,title:'snapshot round-trip',completed:false};
        const expected = {
            correlationId:'00112233-4455-4677-8899-aabbccddeeff',
            data:[task],isSuccess:true,isReady:true,isAuthorized:true,isValid:true,hasExceptions:false,
            validationResults:[],exceptionMessages:[],exceptionStackTrace:'',paging:{page:0,size:0,totalItems:0,totalPages:0}
        };
        const all = new Tasks(); all.setOrigin(origin);
        all.setHttpHeadersCallback(() => ({'X-Correlation-ID':expected.correlationId}));
        const list = await all.perform();
        assert.deepEqual(requests[0].envelope,expected);
        assert.equal(requests[0].method,'GET'); assert.equal(requests[0].url.pathname,'/api/tasks');
        assert.equal(list.isSuccess,true); assert.ok(Array.isArray(list.data)); assert.equal(list.data.length,1);
        assert.ok(list.data[0] instanceof Task); assert.ok(list.data[0].id instanceof Guid);
        assert.equal(list.data[0].id.toString(),id); assert.equal(list.data[0].title,task.title); assert.equal(list.data[0].completed,false);
        const byID = new ByID(); byID.setOrigin(origin);
        byID.setHttpHeadersCallback(() => ({'X-Correlation-ID':expected.correlationId}));
        const one = await byID.perform({id:Guid.parse(id)});
        assert.deepEqual(requests[1].envelope,{...expected,data:task});
        assert.equal(requests[1].method,'GET'); assert.equal(requests[1].url.searchParams.get('id'),id);
        assert.ok(one.data instanceof Task); assert.ok(one.data.id instanceof Guid); assert.equal(one.data.id.toString(),id);
        byID.setHttpMethod(QueryHttpMethod.Query);
        const queryResult = await byID.perform({id:Guid.parse(id)});
        assert.deepEqual(requests[2].envelope,{...expected,data:task}); assert.equal(requests[2].method,'QUERY');
        assert.ok(queryResult.data instanceof Task); assert.equal(queryResult.data.title,task.title);
        assert.equal(requests.length,3);
    } finally {
        globalThis.fetch = originalFetch;
        child.kill('SIGTERM');
        const timer = setTimeout(() => child.kill('SIGKILL'),3000);
        try {const exit = await exited; assert.equal(exit.code,0,stderr);}
        finally {clearTimeout(timer);}
    }
});
