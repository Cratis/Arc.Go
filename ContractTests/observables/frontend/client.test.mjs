// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import test from 'node:test';
import { writeFile, mkdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { build } from 'esbuild';
import WebSocket from 'ws';
import { EventSource } from 'eventsource';
import { Globals, ObservableQueryTransferMode } from '@cratis/arc';
import { QueryTransportMethod, WebSocketHubConnection, ServerSentEventHubConnection, resetSharedMultiplexer, getSharedMultiplexer } from '@cratis/arc/queries';

const origin = process.env.ARC_FIXTURE_ORIGIN;
const queryName = process.env.ARC_FIXTURE_QUERY;
assert.ok(origin && queryName, 'Required real fixture host is missing; use node run.mjs');
assert.match(origin, /^http:\/\/127\.0\.0\.1:\d+$/);
const trace = [];
const sockets = [];
const sources = [];
const nativeFetch = globalThis.fetch;
const output = await build({ entryPoints: [resolve(import.meta.dirname, 'queries.ts')], bundle: true, write: false,
    keepNames: true, platform: 'node', format: 'esm', target: 'es2022', plugins: [{ name: 'pinned-runtime', setup(builder) {
        builder.onResolve({ filter: /^@cratis\// }, args => ({ path: import.meta.resolve(args.path), external: true }));
    } }] });
const { Items, NilItems, Item } = await import('data:text/javascript;base64,' + Buffer.from(output.outputFiles[0].text).toString('base64'));

class Queue {
    values = [];
    waiting = [];
    count = 0;
    push = value => {
        this.count++;
        if (this.waiting.length) this.waiting.shift()(value);
        else this.values.push(value);
    };
    async next() {
        if (this.values.length) return this.values.shift();
        let timer;
        let receive;
        try {
            return await new Promise((resolve, reject) => {
                receive = resolve;
                this.waiting.push(receive);
                timer = setTimeout(() => reject(new Error('Client callback/frame timed out after 3 seconds')), 3000);
            });
        } finally {
            clearTimeout(timer);
            this.waiting = this.waiting.filter(candidate => candidate !== receive);
        }
    }
}
class RecordedSocket extends WebSocket {
    frames = new Queue();
    constructor(url) {
        super(url, { handshakeTimeout: 3000, maxPayload: 1024 * 1024 });
        this.urlRecorded = url;
        sockets.push(this);
        this.addEventListener('message', event => {
            const message = JSON.parse(String(event.data));
            trace.push({ transport: 'ws', direction: 'receive', url, message });
            this.frames.push(message);
        });
    }
    send(data, ...rest) {
        trace.push({ transport: 'ws', direction: 'send', url: this.urlRecorded, message: JSON.parse(String(data)) });
        return super.send(data, ...rest);
    }
}
globalThis.WebSocket = RecordedSocket;
globalThis.EventSource = EventSource;
globalThis.fetch = async (url, options = {}) => {
    const request = { transport: 'http', url: String(url), method: options.method ?? 'GET', body: options.body };
    trace.push(request);
    const response = await nativeFetch(url, { ...options, signal: options.signal ?? AbortSignal.timeout(4000) });
    request.status = response.status;
    return response;
};
Globals.eventSourceFactory = url => {
    const source = new EventSource(url);
    sources.push(source);
    source.addEventListener('message', event => trace.push({ transport: 'sse', direction: 'receive', url, message: JSON.parse(event.data) }));
    return source;
};

const initial = group => [{ id: 'a', title: group + '-old' }, { id: 'b', title: group + '-gone' }];
const updated = group => [{ id: 'a', title: group + '-changed' }, { id: 'c', title: group + '-new' }];
const plain = items => items.map(({ id, title }) => ({ id, title }));
async function control(path, body) {
    const response = await fetch(origin + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    assert.equal(response.status, 204, await response.text());
}
const publish = (group, items) => control('/fixture/publish', { group, items });
async function active(count, group = '') {
    const response = await fetch(`${origin}/fixture/wait?active=${count}&group=${group}`);
    assert.equal(response.status, 204, await response.text());
}
function configure(transport, direct = false, mode = ObservableQueryTransferMode.Delta) {
    resetSharedMultiplexer();
    Globals.origin = origin;
    Globals.apiBasePath = '';
    Globals.queryDirectMode = direct;
    Globals.queryTransportMethod = transport === 'sse' ? QueryTransportMethod.ServerSentEvents : QueryTransportMethod.WebSocket;
    Globals.observableQueryTransferMode = mode;
}
function observe(query, group) {
    const results = new Queue();
    const subscription = query.subscribe(result => {
        trace.push({ direction: 'callback', group, result: JSON.parse(JSON.stringify(result)) });
        results.push(result);
    }, { group });
    return { results, subscription };
}

// This is deliberately a TEST CONSUMER, not an imitation @cratis/arc.react hook.
// The core package forwards changeSet and normalizes omitted enumerable data to
// []; it does not reconstruct the collection. Assert envelopes before reducing.
function applyDelta(previous, changes) {
    const retained = previous.filter(item => !changes.removed.some(removed => removed.id === item.id));
    return [...retained.map(item => changes.replaced.find(replaced => replaced.id === item.id) ?? item), ...changes.added];
}
function assertDelta(result, group) {
    assert.deepEqual(result.data, []);
    assert.deepEqual(result.changeSet, {
        added: [updated(group)[1]], replaced: [updated(group)[0]], removed: [initial(group)[1]]
    });
}
async function queryCase(transport, direct, mode) {
    configure(transport, direct, mode);
    await publish('alpha', initial('alpha'));
    await publish('beta', initial('beta'));
    const startSockets = sockets.length;
    const startSources = sources.length;
    const query = new Items(queryName);
    const sibling = new Items(queryName);
    const a = observe(query, 'alpha');
    const b = observe(sibling, 'beta');
    try {
        const first = await a.results.next();
        assert.equal(first.isReady, true);
        assert.equal(first.isSuccess, true);
        assert.ok(first.data.every(item => item instanceof Item), 'Real Fundamentals hydration');
        assert.deepEqual(plain(first.data), initial('alpha'));
        assert.deepEqual(plain((await b.results.next()).data), initial('beta'));
        await active(2);
        if (!direct) {
            assert.equal(transport === 'ws' ? sockets.length - startSockets : sources.length - startSources, 1, 'Two queries must share one physical hub');
            assert.equal(getSharedMultiplexer().getConnectionsSnapshot()[0].queryCount, 2);
        }
        await publish('alpha', updated('alpha'));
        const next = await a.results.next();
        let collection;
        if (!direct && mode === ObservableQueryTransferMode.Delta) {
            assertDelta(next, 'alpha');
            collection = applyDelta(first.data, next.changeSet);
        } else {
            assert.ok(next.data.every(item => item instanceof Item));
            collection = next.data;
            assert.equal(next.changeSet, undefined);
        }
        assert.deepEqual(plain(collection), updated('alpha'), 'Final test-consumer collection');
        a.subscription.unsubscribe();
        await active(0, 'alpha');
        const callbacks = a.results.count;
        await publish('alpha', [{ id: 'z', title: 'must not resurrect' }]);
        await publish('beta', updated('beta'));
        const siblingNext = await b.results.next();
        assert.equal(siblingNext.isReady, true);
        assert.equal(a.results.count, callbacks, 'Canceled subscription must not receive later emissions');
        if (!direct) assert.equal(getSharedMultiplexer().getConnectionsSnapshot()[0].queryCount, 1);
        b.subscription.unsubscribe();
        await active(0);
    } finally { query.dispose(); sibling.dispose(); resetSharedMultiplexer(); }
}
async function frame(socket, predicate) {
    for (;;) { const message = await socket.frames.next(); if (predicate(message)) return message; }
}
function hub(transport) {
    return transport === 'ws'
        ? new WebSocketHubConnection(origin.replace('http:', 'ws:') + '/.cratis/queries/ws', '')
        : new ServerSentEventHubConnection(origin + '/.cratis/queries/sse', origin + '/.cratis/queries/sse/subscribe', origin + '/.cratis/queries/sse/unsubscribe', '');
}
async function replacementAndLegacy(transport) {
    configure(transport);
    await publish('alpha', initial('alpha'));
    await publish('beta', initial('beta'));
    const connection = hub(transport);
    const a = new Queue();
    const b = new Queue();
    const start = trace.length;
    const request = { queryName, arguments: { group: 'alpha' }, transferMode: 'legacy' };
    try {
        connection.subscribe('replace', request, a.push);
        connection.subscribe('sibling', { ...request, arguments: { group: 'beta' } }, b.push);
        assert.deepEqual((await a.next()).data, initial('alpha'));
        await b.next();
        await active(2);
        await publish('alpha', updated('alpha'));
        const legacy = await a.next();
        assert.deepEqual(legacy.data, updated('alpha'), 'Legacy delivers full data');
        assertDelta({ ...legacy, data: [] }, 'alpha'); // Legacy also delivers the same changeSet.
        // Same ID, newer revision via the real transport API: fresh full baseline
        // must replace its former owner's delta history and stream.
        connection.subscribe('replace', { ...request, arguments: { group: 'beta' }, transferMode: 'delta' }, a.push);
        const replacement = await a.next();
        assert.deepEqual(replacement.data, initial('beta'));
        assert.equal(replacement.changeSet, undefined);
        await active(0, 'alpha');
        await active(2, 'beta');
        connection.unsubscribe('replace');
        await active(1, 'beta');
        const slice = trace.slice(start);
        let subscribe, unsubscribe;
        if (transport === 'ws') {
            const sends = slice.filter(entry => entry.direction === 'send').map(entry => entry.message);
            subscribe = sends.findLast(message => message.type === 'Subscribe' && message.queryId === 'replace');
            unsubscribe = sends.findLast(message => message.type === 'Unsubscribe' && message.queryId === 'replace');
        } else {
            subscribe = JSON.parse(slice.findLast(entry => entry.url.endsWith('/sse/subscribe') && JSON.parse(entry.body).queryId === 'replace').body);
            unsubscribe = JSON.parse(slice.findLast(entry => entry.url.endsWith('/sse/unsubscribe') && JSON.parse(entry.body).queryId === 'replace').body);
        }
        assert.ok(Number.isSafeInteger(subscribe.revision) && subscribe.revision > 1);
        assert.equal(unsubscribe.revision, subscribe.revision, 'Package must unsubscribe the current/equal revision');
        const count = a.count;
        const retiredTrace = trace.length;
        // Negative protocol injection replays an EXACT captured outgoing client
        // Subscribe after its equal-revision unsubscribe, not invented results.
        if (transport === 'ws') {
            const socket = sockets.at(-1);
            socket.send(JSON.stringify(subscribe));
            socket.send(JSON.stringify({ type: 'Ping', timestamp: 123 }));
            await frame(socket, message => message.type === 'Pong' && message.timestamp === 123);
        } else {
            const response = await fetch(origin + '/.cratis/queries/sse/subscribe', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(subscribe) });
            assert.equal(response.status, 200);
            await response.text();
        }
        await active(1, 'beta');
        await publish('beta', updated('beta'));
        await b.next();
        assert.equal(a.count, count, 'Retired owner callback must not resurrect');
        assert.deepEqual(trace.slice(retiredTrace).filter(entry => entry.direction === 'receive' && entry.message.queryId === 'replace'), [], 'Server must not emit a retired owner result');
        assert.equal(connection.queryCount, 1);
        connection.unsubscribe('sibling');
        await active(0);
    } finally { connection.dispose(); }
}

async function runCase(t, name, callback) {
    let failure;
    await t.test(name, async () => {
        try { await callback(); } catch (error) { failure = error; throw error; }
    });
    // Node subtest failures do not reject t.test's promise. Stop subsequent cases
    // and let the outer catch persist the exact evidence on the first failure.
    if (failure) throw failure;
}

// All subcases execute sequentially against a real child process; failures retain
// exact outgoing controls, incoming frames and callbacks, never rewrite goldens.
test('locked Arc observable client conformance (Node, not browser/React)', { timeout: 40000 }, async t => {
    try {
        const lock = JSON.parse(await readFile(new URL('package-lock.json', import.meta.url)));
        for (const [name, version] of Object.entries({ '@cratis/arc': '22.48.2', '@cratis/fundamentals': '7.22.0', ws: '8.22.0', eventsource: '3.0.7' })) {
            assert.equal(lock.packages['node_modules/' + name].version, version);
            assert.ok(lock.packages['node_modules/' + name].integrity);
            const installed = JSON.parse(await readFile(new URL(`node_modules/${name}/package.json`, import.meta.url)));
            assert.equal(installed.version, version, `Installed package ${name} differs from the required pin`);
        }
        // Exercise package defaults before explicitly selecting later modes.
        assert.equal(Globals.queryDirectMode, false);
        assert.equal(Globals.queryTransportMethod, QueryTransportMethod.WebSocket);
        assert.equal(Globals.observableQueryTransferMode, ObservableQueryTransferMode.Delta);
        assert.equal(Globals.queryConnectionCount, 1);
        for (const transport of ['ws', 'sse']) {
            for (const mode of [ObservableQueryTransferMode.Delta, ObservableQueryTransferMode.Full]) {
                await runCase(t, `${transport} hub ${mode}: hydration, changeSet, final collection, cancellation and cleanup`, () => queryCase(transport, false, mode));
            }
            await runCase(t, `${transport} hub legacy, same-ID replacement, equal-revision unsubscribe, stale replay rejection`, () => replacementAndLegacy(transport));
            await runCase(t, `${transport} direct full snapshots and cancellation`, () => queryCase(transport, true, ObservableQueryTransferMode.Delta));
        }
        await runCase(t, 'real query perform distinguishes nil-ready from pending; pending argument replacement joins', async () => {
            configure('ws');
            const nilQuery = new NilItems(queryName);
            const pending = new NilItems(queryName);
            const nilResult = await nilQuery.perform({ group: 'nil' });
            assert.equal(nilResult.isReady, true);
            assert.equal(nilResult.data, null, 'QueryResult constructor normalizes omitted non-enumerable data to null');
            const pendingResult = await pending.perform({ group: 'pending' });
            assert.equal(pendingResult.isReady, false);
            const old = observe(pending, 'pending');
            await active(1, 'pending');
            const replacement = observe(pending, 'nil');
            const ready = await replacement.results.next();
            assert.equal(ready.isReady, true);
            assert.equal(ready.data, undefined);
            await active(0, 'pending');
            await publish('pending', [{ id: 'p', title: 'retired argument' }]);
            assert.equal(old.results.count, 0);
            replacement.subscription.unsubscribe();
            pending.dispose(); nilQuery.dispose(); resetSharedMultiplexer();
            await active(0);
        });
        await runCase(t, 'all four real transports receive terminal unauthorized and join; no retry/resurrection', async () => {
            const observations = [];
            for (const transport of ['ws', 'sse']) for (const direct of [false, true]) {
                configure(transport, direct);
                // Do not invalidate the previous hub singleton: separate transports
                // below are built with package transport APIs, each explicitly owned.
                if (!direct) {
                    const conn = hub(transport);
                    const results = new Queue();
                    conn.subscribe('terminal', { queryName, arguments: { group: 'guarded' }, transferMode: 'full' }, results.push);
                    await results.next();
                    observations.push({ results, dispose: () => conn.dispose() });
                } else {
                    const query = new Items(queryName);
                    const observed = observe(query, 'guarded');
                    await observed.results.next();
                    observations.push({ results: observed.results, dispose: () => query.dispose() });
                }
            }
            try {
                await active(4, 'guarded');
                await control('/fixture/deny', {});
                await publish('guarded', updated('guarded'));
                for (const observation of observations) {
                    const terminal = await observation.results.next();
                    assert.equal(terminal.isAuthorized, false);
                    assert.equal(terminal.isSuccess, false);
                }
                await active(0);
                assert.equal(sources.at(-1).readyState, EventSource.CLOSED, 'Direct SSE must close terminal EventSource');
                const counts = observations.map(observation => observation.results.count);
                await publish('guarded', [{ id: 'z', title: 'after termination' }]);
                assert.deepEqual(observations.map(observation => observation.results.count), counts);
            } finally { for (const observation of observations) observation.dispose(); }
        });
    } catch (error) {
        const directory = resolve(import.meta.dirname, '../../../.ai-work/keep');
        await mkdir(directory, { recursive: true });
        await writeFile(resolve(directory, 'observable-client-failure.json'), JSON.stringify({ error: String(error), origin, queryName, trace }, null, 2) + '\n');
        throw error;
    } finally {
        resetSharedMultiplexer();
        for (const socket of sockets) socket.close();
        for (const source of sources) source.close();
        globalThis.fetch = nativeFetch;
    }
});
