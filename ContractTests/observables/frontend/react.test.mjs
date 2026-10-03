// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import React from 'react';
import TestRenderer from 'react-test-renderer';
import { build } from 'esbuild';
import WebSocket from 'ws';
import { Globals, ObservableQueryTransferMode } from '@cratis/arc';
import { ArcContext } from '@cratis/arc.react';
import { QueryInstanceCacheContext } from '@cratis/arc.react/queries';
import { QueryInstanceCache, QueryTransportMethod, Sorting, SortDirection, resetSharedMultiplexer, getSharedMultiplexer } from '@cratis/arc/queries';

const { act, create } = TestRenderer;
const origin = process.env.ARC_FIXTURE_ORIGIN;
assert.ok(origin && process.env.ARC_FIXTURE_QUERY, 'Use node run.mjs react-runtime with the real generated fixture host');
// External URLs resolve from THIS locked installation, including React. Only
// production-generated application code is bundled; no hook or reducer clone.
const output = await build({ entryPoints: [resolve(import.meta.dirname, 'Generated/Contracts/Items/index.ts')],
    bundle: true, write: false, keepNames: true, platform: 'node', format: 'esm', target: 'es2022',
    plugins: [{ name: 'one-locked-runtime', setup(builder) {
        builder.onResolve({ filter: /^(?:@cratis\/|react(?:\/|$))/ }, args => ({ path: import.meta.resolve(args.path), external: true }));
    } }] });
const { All, Item } = await import('data:text/javascript;base64,' + Buffer.from(output.outputFiles[0].text).toString('base64'));
const trace = [];
const sockets = [];
const roots = new Set();
const caches = new Set();
const requests = new Set();
const buses = [];
const handled = promise => { promise.catch(() => {}); return promise; };

// One absolute 3s deadline per predicate, not a fresh timeout per unrelated frame.
class Signals {
    history = []; waiting = new Set();
    constructor(label) { this.label = label; buses.push(this); }
    push(value) {
        this.history.push(value);
        for (const waiter of this.waiting) if (waiter.predicate(value)) waiter.finish(undefined, value);
    }
    wait(predicate, after = this.history.length) {
        return handled(new Promise((resolve, reject) => {
            const waiter = { predicate, finish: (error, value) => {
                clearTimeout(timer); this.waiting.delete(waiter);
                if (error) reject(error); else resolve(value);
            } };
            const timer = setTimeout(() => waiter.finish(new Error(`${this.label} deadline exceeded`)), 3000);
            const existing = this.history.slice(after).find(predicate);
            if (existing !== undefined) waiter.finish(undefined, existing); else this.waiting.add(waiter);
        }));
    }
    cancel() { for (const waiter of this.waiting) waiter.finish(new Error(`${this.label} canceled during cleanup`)); }
}
const frames = new Signals('WebSocket frame');
class RecordedSocket extends WebSocket {
    constructor(url) {
        super(url, { handshakeTimeout: 3000, maxPayload: 1024 * 1024 });
        sockets.push(this);
        // Record before the stock client installs its listener, without mutating frames.
        this.addEventListener('message', event => {
            const message = JSON.parse(String(event.data));
            trace.push({ direction: 'receive', message }); frames.push(message);
        });
        this.on('error', error => trace.push({ direction: 'socket-error', error: String(error) }));
    }
    send(data, ...rest) {
        trace.push({ direction: 'send', message: JSON.parse(String(data)) });
        return super.send(data, ...rest);
    }
}
const priorSocket = globalThis.WebSocket;
const priorGlobals = Object.fromEntries(['origin', 'apiBasePath', 'queryTransportMethod', 'queryDirectMode', 'queryConnectionCount', 'observableQueryTransferMode'].map(key => [key, Globals[key]]));
const configuration = { microservice: '', origin, apiBasePath: '', queryVersion: 0,
    queryTransportMethod: QueryTransportMethod.WebSocket, queryDirectMode: false,
    queryConnectionCount: 1, observableQueryTransferMode: ObservableQueryTransferMode.Delta };
async function request(path, body, status = 204) {
    const controller = new AbortController(); requests.add(controller);
    const timer = setTimeout(() => controller.abort(), 4000);
    try {
        const response = await fetch(origin + path, { signal: controller.signal,
            ...(body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }) });
        assert.equal(response.status, status, await response.clone().text());
        return status === 200 ? response.json() : undefined;
    } finally { clearTimeout(timer); requests.delete(controller); }
}
const signals = () => request('/fixture/signals', undefined, 200);
const publish = (group, items) => request('/fixture/publish', { group, items });
const active = (count, group = '') => handled(request(`/fixture/wait?active=${count}&group=${group}`));
const event = (name, count) => handled(request(`/fixture/wait?event=${name}&count=${count}`));
let barrierID = 0;
async function barrier() {
    const timestamp = ++barrierID;
    const pong = frames.wait(frame => frame.type === 'Pong' && frame.timestamp === timestamp);
    sockets.at(-1).send(JSON.stringify({ type: 'Ping', timestamp }));
    await pong;
}
const rich = (id, title, rank = 7) => ({ id, title, createdAt: '2026-10-03T12:00:00Z', rank, enabled: true });
const initial = group => [rich('a', `${group}-old`), rich('b', `${group}-gone`, 6)];
const changed = group => [rich('a', `${group}-changed`, 8), rich('c', `${group}-new`, 3)];
const plain = items => items.map(({ id, title }) => ({ id, title }));
const same = (items, expected) => JSON.stringify(plain(items)) === JSON.stringify(plain(expected));
function assertRich(items, expected) {
    assert.deepEqual(plain(items), plain(expected));
    for (let i = 0; i < items.length; i++) {
        assert.ok(items[i] instanceof Item);
        assert.ok(items[i].createdAt instanceof Date);
        assert.equal(items[i].createdAt.toISOString(), '2026-10-03T12:00:00.000Z');
        assert.equal(items[i].rank, expected[i].rank); assert.equal(items[i].enabled, expected[i].enabled);
    }
}
function assertChanges(actual, expected) {
    for (const key of ['added', 'replaced', 'removed']) assertRich(actual[key], expected[key]);
}
const getKey = item => item.id;
// Fixed hook shape, unconditional calls. A post-commit effect exposes actual
// hook-returned values; render counts are never interpreted as delivery counts.
function SharedHooks({ args, commits }) {
    const tuple = All.use(args);
    const changes = All.useChangeStream(args, getKey);
    React.useEffect(() => { trace.push({ direction: 'commit', family: 'shared', result: tuple[0], changes }); commits.push({ tuple, changes }); });
    return null;
}
function PagedHook({ args, commits }) {
    const tuple = All.useWithPaging(1, args);
    React.useEffect(() => { trace.push({ direction: 'commit', family: 'paged', result: tuple[0] }); commits.push({ tuple }); });
    return null;
}
function SingleHook({ args, commits }) {
    const tuple = All.use(args);
    React.useEffect(() => { trace.push({ direction: 'commit', family: 'single', result: tuple[0] }); commits.push({ tuple }); });
    return null;
}
function tree(Component, cache, args, commits) {
    return React.createElement(ArcContext.Provider, { value: configuration },
        React.createElement(QueryInstanceCacheContext.Provider, { value: cache }, React.createElement(Component, { args, commits })));
}
function cache(retention) { const value = retention === undefined ? new QueryInstanceCache() : new QueryInstanceCache(retention); caches.add(value); return value; }
async function mount(Component, value, args, commits) {
    let root;
    // Flush setup effects FIRST; awaiting a baseline inside this act callback
    // would deadlock effects which have not yet been installed.
    await act(async () => { root = create(tree(Component, value, args, commits)); roots.add(root); });
    return root;
}
async function update(root, Component, value, args, commits) {
    await act(async () => { root.update(tree(Component, value, args, commits)); });
}
async function unmount(root) { await act(async () => { root.unmount(); roots.delete(root); }); }
async function committed(promise) {
    // Await a transport barrier inside act, then let act flush passive effects.
    // Waiting for a post-commit effect INSIDE act would deadlock that flush.
    await act(async () => { await barrier(); });
    return promise;
}
const ready = (commits, expected) => commits.wait(value => value.tuple[0].isReady && value.tuple[0].isSuccess && same(value.tuple[0].data, expected));
const subscribes = after => trace.slice(after).filter(entry => entry.direction === 'send' && entry.message.type === 'Subscribe').map(entry => entry.message);
function entry(value, group) { return value.getDiagnosticsSnapshot().entries.find(e => e.key === value.buildKey(process.env.ARC_FIXTURE_QUERY, { group })); }
async function retire(root, value, group) {
    const before = await signals();
    const closed = event('close', before.close + 1), disposed = event('dispose', before.dispose + 1), inactive = active(0, group);
    await unmount(root);
    // retention=0 still defers via setTimeout(0). These must pass BEFORE any
    // fallback cache.dispose(), so cleanup cannot manufacture the evidence.
    await Promise.all([closed, disposed, inactive]);
    assert.equal(value.getDiagnosticsSnapshot().entryCount, 0);
}

// Non-StrictMode renderer + explicitly configured WebSocket providers, NOT the
// complete <Arc> wrapper (whose default transport is SSE) or a browser lane.
test('mounted generated hook family over real WebSocket Delta', { timeout: 45000 }, async t => {
    let warmQuery, warmSubscription;
    async function runCase(name, action) {
        let failure;
        await t.test(name, async () => {
            try { await action(); } catch (error) { failure = error; throw error; }
        });
        if (failure) throw failure; // Stop dependent cases and retain exact evidence.
    }
    try {
        const lock = JSON.parse(await readFile(new URL('package-lock.json', import.meta.url)));
        const reference = JSON.parse(await readFile(new URL('../../ProxyComparison/package-lock.json', import.meta.url)));
        for (const name of ['@cratis/arc', '@cratis/arc.react', '@cratis/fundamentals', 'react', 'react-dom']) {
            assert.equal(lock.packages[`node_modules/${name}`].version, reference.packages[`node_modules/${name}`].version);
            assert.equal(lock.packages[`node_modules/${name}`].integrity, reference.packages[`node_modules/${name}`].integrity);
        }
        const renderer = JSON.parse(await readFile(new URL('node_modules/react-test-renderer/package.json', import.meta.url)));
        assert.equal(renderer.version, '18.3.1'); assert.equal(renderer.peerDependencies.react, '^18.3.1');
        assert.equal(new All().queryName, process.env.ARC_FIXTURE_QUERY);
        globalThis.WebSocket = RecordedSocket;
        // ArcContext.Provider alone does not configure Globals like <Arc> does.
        Object.assign(Globals, { origin, apiBasePath: '', queryTransportMethod: QueryTransportMethod.WebSocket,
            queryDirectMode: false, queryConnectionCount: 1, observableQueryTransferMode: ObservableQueryTransferMode.Delta });
        warmQuery = new All(); warmQuery.setOrigin(origin);
        const warmResults = new Signals('warm generated callback');
        const firstWarm = warmResults.wait(result => result.isReady);
        const connected = frames.wait(frame => frame.type === 'Connected' && frame.supportsSubscriptionRevisions);
        warmSubscription = warmQuery.subscribe(result => warmResults.push(result), { group: 'guarded' });
        await Promise.all([firstWarm, connected]); await barrier(); await active(1);
        const warmBaseline = await signals();
        // Account for the real pre-Connected subscription and its replacement.
        await event('dispose', warmBaseline.close);
        assert.equal(sockets.length, 1);

        await runCase('use and useChangeStream share a cache and reconstruct rich delta collections', async () => {
            const value = cache(0), commits = new Signals('shared hook commit');
            const before = await signals(), start = trace.length;
            const baseline = ready(commits, initial('alpha'));
            const root = await mount(SharedHooks, value, { group: 'alpha' }, commits);
            const first = await committed(baseline);
            assert.equal(first.tuple.length, 2); assert.equal(typeof first.tuple[1], 'function');
            assertRich(first.tuple[0].data, initial('alpha'));
            // The stream's own effect may commit after the observable state.
            const firstChanges = await committed(commits.wait(v => same(v.changes.added, initial('alpha')), 0));
            assertChanges(firstChanges.changes, { added: initial('alpha'), replaced: [], removed: [] });
            await event('open', before.open + 1); await active(1, 'alpha'); await barrier();
            const counters = await signals();
            assert.equal(counters.resolve - before.resolve, 1); assert.equal(counters.factory - before.factory, 1);
            assert.equal(entry(value, 'alpha').subscriberCount, 2); assert.equal(entry(value, 'alpha').listenerCount, 2);
            assert.equal(getSharedMultiplexer().getConnectionsSnapshot()[0].queryCount, 2, 'Warm-up plus one shared hook subscription');
            const subscribe = subscribes(start); assert.equal(subscribe.length, 1);
            assert.deepEqual(subscribe[0].payload.arguments, { group: 'alpha' });
            assert.ok(subscribe[0].revision > 0);
            const expected = { added: [changed('alpha')[1]], replaced: [changed('alpha')[0]], removed: [initial('alpha')[1]] };
            const raw = frames.wait(f => f.type === 'QueryResult' && f.queryId === subscribe[0].queryId && f.payload.changeSet);
            const delta = commits.wait(v => same(v.tuple[0].data, changed('alpha')) && same(v.changes.added, expected.added));
            await act(async () => { await publish('alpha', changed('alpha')); await raw; });
            const frame = await raw;
            assert.equal(frame.payload.data, undefined, 'Raw delta omits the full collection');
            assert.deepEqual(frame.payload.changeSet, expected);
            const next = await delta;
            assertRich(next.tuple[0].data, changed('alpha'));
            assertChanges(next.tuple[0].changeSet, expected); assertChanges(next.changes, expected);

            const unchanged = await signals(), unchangedStart = trace.length;
            await update(root, SharedHooks, value, { group: 'alpha' }, commits); await barrier();
            assert.equal(subscribes(unchangedStart).length, 0, 'Fresh but equal arguments do not reopen');
            assert.deepEqual(await signals(), unchanged);
            assert.equal(entry(value, 'alpha').listenerCount, 2);

            const replacementStart = trace.length;
            const betaReady = ready(commits, initial('beta'));
            const crossExpected = { added: [initial('beta')[1]], replaced: [initial('beta')[0]], removed: [changed('alpha')[1]] };
            const cross = commits.wait(v => same(v.tuple[0].data, initial('beta')) && same(v.changes.replaced, crossExpected.replaced));
            const closed = event('close', counters.close + 1), disposed = event('dispose', counters.dispose + 1), inactive = active(0, 'alpha');
            await update(root, SharedHooks, value, { group: 'beta' }, commits);
            await committed(betaReady); const beta = await committed(cross);
            await Promise.all([closed, disposed, inactive]); await active(1, 'beta'); await barrier();
            assertRich(beta.tuple[0].data, initial('beta'));
            assert.equal(beta.tuple[0].changeSet, undefined, 'Replacement is a raw full baseline');
            assertChanges(beta.changes, crossExpected); // Compare baselines, not an invented all-added reset.
            const replacement = subscribes(replacementStart); assert.equal(replacement.length, 1);
            assert.notEqual(replacement[0].queryId, subscribe[0].queryId);
            const retiredStart = frames.history.length;
            const liveRaw = frames.wait(f => f.type === 'QueryResult' && f.queryId === replacement[0].queryId && f.payload.changeSet);
            const live = commits.wait(v => same(v.tuple[0].data, changed('beta')) && same(v.changes.added, [changed('beta')[1]]));
            await act(async () => {
                await publish('alpha', [rich('z', 'retired')]); await publish('beta', changed('beta'));
                await liveRaw;
            });
            await barrier();
            assert.equal(frames.history.slice(retiredStart).filter(f => f.queryId === subscribe[0].queryId).length, 0);
            assertRich((await live).tuple[0].data, changed('beta'));
            assert.equal(value.has(value.buildKey(process.env.ARC_FIXTURE_QUERY, { group: 'alpha' })), false);
            await retire(root, value, 'beta');
        });

        await runCase('useWithPaging tuple/request and reconstruction; characterize retained same-key setters', async () => {
            await publish('alpha', initial('alpha')); await publish('beta', initial('beta'));
            const value = cache(0), commits = new Signals('paged hook commit');
            const start = trace.length, firstReady = ready(commits, initial('alpha'));
            const root = await mount(PagedHook, value, { group: 'alpha' }, commits);
            const first = await committed(firstReady); await barrier(); await active(1, 'alpha');
            assert.equal(first.tuple.length, 4);
            for (const setter of first.tuple.slice(1)) assert.equal(typeof setter, 'function');
            assertRich(first.tuple[0].data, initial('alpha'));
            const subscribe = subscribes(start); assert.equal(subscribe.length, 1);
            assert.equal(subscribe[0].payload.page, 0); assert.equal(subscribe[0].payload.pageSize, 1);
            // Plain fixture slices deliberately are NOT server-side paging proof.
            assert.equal(first.tuple[0].data.length, 2);
            const deltaReady = ready(commits, changed('alpha'));
            const raw = frames.wait(f => f.type === 'QueryResult' && f.queryId === subscribe[0].queryId && f.payload.changeSet);
            await act(async () => { await publish('alpha', changed('alpha')); await raw; });
            assertRich((await deltaReady).tuple[0].data, changed('alpha'));

            // Minimal pinned-client characterization (Cratis/Arc#2869): setters update hook state
            // but reacquire the subscribed cache key, so no new request is sent.
            // Do not repair this by evicting the cache or changing the Go host.
            const settersStart = trace.length, beforeSetters = await signals();
            await act(async () => { await first.tuple[2](1); });
            await act(async () => { await commits.history.at(-1).tuple[3](2); });
            await act(async () => { await commits.history.at(-1).tuple[1](new Sorting('title', SortDirection.descending)); });
            await barrier();
            assert.equal(subscribes(settersStart).length, 0, '22.48.2 retains the same-key subscription after paging/sorting setters');
            assert.deepEqual(await signals(), beforeSetters);
            const replacementStart = trace.length, betaReady = ready(commits, initial('beta'));
            const closed = event('close', beforeSetters.close + 1), disposed = event('dispose', beforeSetters.dispose + 1), inactive = active(0, 'alpha');
            await update(root, PagedHook, value, { group: 'beta' }, commits);
            const beta = await committed(betaReady); await Promise.all([closed, disposed, inactive]); await barrier();
            assertRich(beta.tuple[0].data, initial('beta'));
            const replacement = subscribes(replacementStart); assert.equal(replacement.length, 1);
            assert.deepEqual(replacement[0].payload.arguments, { group: 'beta' });
            assert.equal(replacement[0].payload.page, 1); assert.equal(replacement[0].payload.pageSize, 2);
            assert.equal(replacement[0].payload.sortBy, 'title'); assert.equal(replacement[0].payload.sortDirection, 'desc');
            await retire(root, value, 'beta');
        });

        await runCase('default retention reuses subscription after unmount, then explicitly disposes', async () => {
            await publish('alpha', initial('alpha'));
            const value = cache(), commits = new Signals('retained hook commit');
            const before = await signals(), start = trace.length, firstReady = ready(commits, initial('alpha'));
            const root = await mount(SingleHook, value, { group: 'alpha' }, commits);
            await committed(firstReady); await active(1, 'alpha'); await barrier();
            const mounted = await signals();
            assert.equal(mounted.factory - before.factory, 1);
            await unmount(root); await barrier();
            assert.equal(entry(value, 'alpha').listenerCount, 0); assert.equal(entry(value, 'alpha').subscriberCount, 0);
            assert.equal(entry(value, 'alpha').subscribed, true); assert.equal(entry(value, 'alpha').hasResult, true);
            assert.deepEqual(await signals(), mounted);
            const reusedReady = ready(commits, initial('alpha'));
            const reused = await mount(SingleHook, value, { group: 'alpha' }, commits);
            assertRich((await committed(reusedReady)).tuple[0].data, initial('alpha')); await barrier();
            assert.equal(subscribes(start).length, 1); assert.deepEqual(await signals(), mounted);
            assert.equal(entry(value, 'alpha').listenerCount, 1);
            await unmount(reused);
            const closed = event('close', mounted.close + 1), disposed = event('dispose', mounted.dispose + 1), inactive = active(0, 'alpha');
            value.dispose(); await Promise.all([closed, disposed, inactive]);
            assert.equal(value.getDiagnosticsSnapshot().entryCount, 0);
            // This proves retention/reuse and explicit disposal, NOT 30s expiry.
        });
    } catch (error) {
        const directory = resolve(import.meta.dirname, '../../../.ai-work/keep'); await mkdir(directory, { recursive: true });
        await writeFile(resolve(directory, 'react-observable-client-failure.json'), JSON.stringify({ error: String(error), stack: error.stack, trace,
            caches: [...caches].map(value => value.getDiagnosticsSnapshot()), signals: await signals().catch(e => ({ error: String(e) })) }, null, 2) + '\n');
        throw error;
    } finally {
        // Fallback owns all resources even if an assertion failed. Natural
        // unmount cleanup above has already been asserted independently.
        try {
            for (const root of roots) await unmount(root);
        } finally {
            for (const value of caches) { value.cancelPendingDispose(); value.dispose(); }
            warmSubscription?.unsubscribe(); warmQuery?.dispose();
            for (const controller of requests) controller.abort();
            for (const bus of buses) bus.cancel();
            const joins = sockets.map(socket => socket.readyState === WebSocket.CLOSED ? Promise.resolve() : handled(new Promise((resolve, reject) => {
                const timer = setTimeout(() => { socket.terminate(); reject(new Error('WebSocket close join deadline exceeded')); }, 3000);
                socket.once('close', () => { clearTimeout(timer); resolve(); });
            })));
            resetSharedMultiplexer(); for (const socket of sockets) socket.close();
            try { await Promise.all(joins); await active(0); }
            finally { Object.assign(Globals, priorGlobals); globalThis.WebSocket = priorSocket; }
        }
    }
});
