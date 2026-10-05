// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { build } from 'esbuild';
import WebSocket from 'ws';
import { Globals, ObservableQueryTransferMode } from '@cratis/arc';
import { QueryTransportMethod, resetSharedMultiplexer, getSharedMultiplexer } from '@cratis/arc/queries';

const origin = process.env.ARC_FIXTURE_ORIGIN;
const queryName = process.env.ARC_FIXTURE_QUERY;
assert.ok(origin && queryName, 'Use node run.mjs generated-runtime with the real fixture host');
const trace = [];
const sockets = [];
const output = await build({ entryPoints: [resolve(import.meta.dirname, 'Generated/Contracts/Items/index.ts')],
    bundle: true, write: false, keepNames: true, platform: 'node', format: 'esm', target: 'es2022',
    plugins: [{ name: 'locked-runtime', setup(builder) {
        builder.onResolve({ filter: /^@cratis\// }, args => ({ path: import.meta.resolve(args.path), external: true }));
    } }] });
const { All, Item, Private } = await import('data:text/javascript;base64,' + Buffer.from(output.outputFiles[0].text).toString('base64'));

class Queue {
    values = []; waiting = []; count = 0;
    push = value => { this.count++; if (this.waiting.length) this.waiting.shift()(value); else this.values.push(value); };
    async next() {
        if (this.values.length) return this.values.shift();
        let timer, receive;
        try { return await new Promise((resolve, reject) => {
            receive = resolve; this.waiting.push(receive);
            timer = setTimeout(() => reject(new Error('Generated callback/protocol barrier timed out')), 3000);
        }); } finally { clearTimeout(timer); this.waiting = this.waiting.filter(value => value !== receive); }
    }
}
class RecordedSocket extends WebSocket {
    frames = new Queue();
    constructor(url) {
        super(url, { handshakeTimeout: 3000, maxPayload: 1024 * 1024 });
        sockets.push(this);
        this.addEventListener('message', event => {
            const message = JSON.parse(String(event.data)); trace.push({ direction: 'receive', message }); this.frames.push(message);
        });
    }
    send(data, ...rest) {
        trace.push({ direction: 'send', message: JSON.parse(String(data)) }); return super.send(data, ...rest);
    }
}
globalThis.WebSocket = RecordedSocket;
async function control(path, body) {
    const response = await fetch(origin + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: AbortSignal.timeout(4000) });
    assert.equal(response.status, 204, await response.text());
}
const publish = (group, items) => control('/fixture/publish', { group, items });
async function active(count, group = '') {
    const response = await fetch(`${origin}/fixture/wait?active=${count}&group=${group}`, { signal: AbortSignal.timeout(4000) });
    assert.equal(response.status, 204, await response.text());
}
async function event(name, count) {
    const response = await fetch(`${origin}/fixture/wait?event=${name}&count=${count}`, { signal: AbortSignal.timeout(4000) });
    assert.equal(response.status, 204, await response.text());
}
async function signals() {
    const response = await fetch(origin + '/fixture/signals', { signal: AbortSignal.timeout(4000) });
    assert.equal(response.status, 200); return response.json();
}
let barrierID = 0;
async function barrier() {
    const timestamp = ++barrierID;
    const socket = sockets.at(-1);
    socket.send(JSON.stringify({ type: 'Ping', timestamp }));
    for (;;) { const frame = await socket.frames.next(); if (frame.type === 'Pong' && frame.timestamp === timestamp) return; }
}
function observe(query, group) {
    const results = new Queue();
    const subscription = query.subscribe(result => { trace.push({ direction: 'callback', group, result }); results.push(result); }, { group });
    return { results, subscription };
}
const plain = items => items.map(item => ({ id: item.id, title: item.title }));
// Independent test-consumer reducer, NOT a mounted React hook or core-client reducer.
function apply(previous, changes) {
    return [...previous.filter(item => !changes.removed.some(removed => removed.id === item.id))
        .map(item => changes.replaced.find(replaced => replaced.id === item.id) ?? item), ...changes.added];
}
const rich = (id, title, rank) => ({ id, title, createdAt: '2026-10-03T12:00:00Z', rank, enabled: true });

test('production-generated observable class: untouched default WebSocket hub + Delta', { timeout: 30000 }, async () => {
    const query = new All();
    const sibling = new All();
    const terminalQuery = new All();
    const pendingQuery = new All();
    const deniedQuery = new Private();
    try {
        const reference = JSON.parse(await readFile(new URL('../../ProxyComparison/package-lock.json', import.meta.url)));
        const lock = JSON.parse(await readFile(new URL('package-lock.json', import.meta.url)));
        for (const name of ['@cratis/arc', '@cratis/arc.react', '@cratis/fundamentals', 'react', 'react-dom', '@types/react', '@types/react-dom']) {
            assert.equal(lock.packages['node_modules/' + name].version, reference.packages['node_modules/' + name].version);
            assert.equal(lock.packages['node_modules/' + name].integrity, reference.packages['node_modules/' + name].integrity);
            const installed = JSON.parse(await readFile(new URL(`node_modules/${name}/package.json`, import.meta.url)));
            assert.equal(installed.version, lock.packages['node_modules/' + name].version);
        }
        assert.equal(query.queryName, queryName, 'Literal generated identity must match generated Go adapter');
        assert.equal(query.route, '/items');
        assert.equal(Globals.queryDirectMode, false);
        assert.equal(Globals.queryTransportMethod, QueryTransportMethod.WebSocket);
        assert.equal(Globals.observableQueryTransferMode, ObservableQueryTransferMode.Delta);
        assert.equal(Globals.queryConnectionCount, 1);
        Globals.origin = origin; Globals.apiBasePath = '';
        // Instances capture origin at construction. Set only the supported origin;
        // recreate them without modifying any transport defaults.
        for (const instance of [query, sibling, terminalQuery, pendingQuery, deniedQuery]) instance.setOrigin(origin);
        // Stock 22.48.2 can send an unrevisioned Subscribe before Connected,
        // then resubmit it with negotiated revisions. Warm and retire one real
        // generated subscription before counting the revision-guaranteed cases.
        const warmQuery = new All();
        const warm = observe(warmQuery, 'alpha'); await warm.results.next();
        await barrier(); await active(1);
        const baseline = await signals();
        const traceStart = trace.length;
        const a = observe(query, 'alpha');
        const b = observe(sibling, 'beta');
        const first = await a.results.next();
        const second = await b.results.next();
        for (const result of [first, second]) {
            assert.equal(result.isReady, true); assert.equal(result.isSuccess, true);
            assert.ok(result.data.every(item => item instanceof Item));
            assert.ok(result.data[0].createdAt instanceof Date, 'Real Fundamentals Date hydration');
            assert.equal(result.data[0].createdAt.toISOString(), '2026-10-03T12:00:00.000Z');
            assert.equal(result.data[0].rank, 7); assert.equal(result.data[0].enabled, true);
        }
        let aState = first.data;
        let bState = second.data;
        assert.deepEqual(plain(aState), [{ id: 'a', title: 'alpha-old' }, { id: 'b', title: 'alpha-gone' }]);
        assert.deepEqual(plain(bState), [{ id: 'a', title: 'beta-old' }, { id: 'b', title: 'beta-gone' }]);
        await active(3); await event('factory', baseline.factory + 2); await event('open', baseline.open + 2);
        warm.subscription.unsubscribe(); await active(2); await barrier(); warmQuery.dispose();
        baseline.close++;
        assert.equal(sockets.length, 1, 'Two generated queries share one physical default hub');
        assert.equal(getSharedMultiplexer().getConnectionsSnapshot()[0].queryCount, 2);
        const alpha = [rich('a', 'alpha-changed', 8), rich('c', 'alpha-new', 3)];
        await publish('alpha', alpha);
        const change = await a.results.next();
        assert.deepEqual(change.data, []);
        assert.deepEqual(change.changeSet, { added: [alpha[1]], replaced: [alpha[0]], removed: [{ ...JSON.parse(JSON.stringify(first.data[1])), createdAt: '2026-10-03T12:00:00Z' }] });
        aState = apply(aState, change.changeSet);
        assert.deepEqual(plain(aState), plain(alpha));
        await barrier();
        assert.equal(a.results.count, 2, 'Exactly one baseline and one accepted delta callback');
        assert.equal(b.results.count, 1, 'Independent sibling has no alpha callback');
        const oldCount = a.results.count;
        const replacement = observe(query, 'beta');
        const replacementFirst = await replacement.results.next();
        assert.deepEqual(plain(replacementFirst.data), plain(second.data));
        assert.equal(replacementFirst.changeSet, undefined, 'Argument replacement starts a fresh full baseline');
        await active(0, 'alpha'); await active(2, 'beta'); await event('close', baseline.close + 1);
        const sends = trace.slice(traceStart).filter(entry => entry.direction === 'send').map(entry => entry.message);
        const subscribes = sends.filter(message => message.type === 'Subscribe');
        assert.equal(subscribes.length, 3);
        assert.deepEqual(subscribes.map(message => message.payload.arguments), [{ group: 'alpha' }, { group: 'beta' }, { group: 'beta' }]);
        const retired = subscribes[0];
        assert.ok(Number.isSafeInteger(retired.revision) && retired.revision > 0);
        assert.equal(sends.find(message => message.type === 'Unsubscribe' && message.queryId === retired.queryId).revision, retired.revision);
        assert.notEqual(subscribes[2].queryId, retired.queryId, 'Stock query argument replacement owns a new subscription ID');
        replacement.subscription.unsubscribe(); await active(1, 'beta');
        const replacementCount = replacement.results.count;
        const beta = [rich('a', 'beta-changed', 9), rich('c', 'beta-new', 4)];
        await publish('alpha', [rich('z', 'canceled', 1)]); await publish('beta', beta);
        const bChange = await b.results.next();
        assert.deepEqual(bChange.changeSet, { added: [beta[1]], replaced: [beta[0]], removed: [{ ...JSON.parse(JSON.stringify(second.data[1])), createdAt: '2026-10-03T12:00:00Z' }] });
        bState = apply(bState, bChange.changeSet);
        assert.deepEqual(plain(bState), plain(beta));
        assert.deepEqual(plain(aState), plain(alpha), 'Independent consumer state does not follow sibling changes');
        await barrier();
        assert.equal(a.results.count, oldCount); assert.equal(replacement.results.count, replacementCount);
        assert.equal(b.results.count, 2);
        const terminal = observe(terminalQuery, 'guarded'); await terminal.results.next();
        b.subscription.unsubscribe(); await active(1); await event('close', baseline.close + 3);
        await control('/fixture/deny', {}); await publish('guarded', [rich('x', 'denied', 1)]);
        const unauthorized = await terminal.results.next();
        assert.equal(unauthorized.isAuthorized, false); assert.equal(unauthorized.isSuccess, false);
        await active(0); await event('close', baseline.close + 4);
        await publish('guarded', [rich('z', 'no resurrection', 1)]); await barrier();
        assert.equal(terminal.results.count, 2, 'One baseline, one terminal result, no resurrection');
        // Leave one real pending source for SIGTERM: runner requires joined host
        // shutdown and cumulative opens == closes, not only an empty active map.
        const pending = observe(pendingQuery, 'pending'); await active(1, 'pending'); await event('open', baseline.open + 5);
        const final = await signals();
        assert.equal(final.resolve - baseline.resolve, 5); assert.equal(final.factory - baseline.factory, 5);
        await barrier();
        const closed = new Promise((resolve, reject) => {
            const timer = setTimeout(() => reject(new Error('Host shutdown did not close the shared socket')), 5000);
            sockets.at(-1).once('close', () => { clearTimeout(timer); resolve(); });
        });
        await control('/fixture/shutdown', {}); await closed;
        assert.equal(pending.results.count, 0, 'Pending shutdown invents no successful result');
    } catch (error) {
        const directory = resolve(import.meta.dirname, '../../../.ai-work/keep'); await mkdir(directory, { recursive: true });
        await writeFile(resolve(directory, 'generated-observable-client-failure.json'), JSON.stringify({ error: String(error), trace }, null, 2) + '\n');
        throw error;
    } finally {
        // Pending source was host-owned until the shutdown barrier above.
        query.dispose(); sibling.dispose(); terminalQuery.dispose(); pendingQuery.dispose(); deniedQuery.dispose();
        resetSharedMultiplexer(); for (const socket of sockets) socket.close();
    }
});
