// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { after, before, test } from 'node:test';
import { chromium } from 'playwright-core';

const assets = process.env.ARC_BROWSER_ASSETS;
const hostExecutable = process.env.ARC_BROWSER_HOST;
assert.ok(assets && hostExecutable, 'Use node run.mjs runtime after the bundle and host-build stages');
const evidencePath = resolve(import.meta.dirname, '../../.ai-work/keep/browser-observable-failure.json');
const hubSSE = '/.cratis/queries/sse';
const hubWS = '/.cratis/queries/ws';
const cookiePrefix = 'cratis-observable-';
const createdAt = '2026-10-03T12:00:00Z';
const failures = [];
let browser;

before(async () => {
    // A missing pinned Chromium fails here; the lane never skips.
    browser = await chromium.launch();
});
after(async () => {
    await browser?.close();
    if (failures.length) {
        await mkdir(resolve(evidencePath, '..'), { recursive: true });
        await writeFile(evidencePath, JSON.stringify(failures, null, 2));
    }
});

// Items arrive in order. Each wait has one absolute deadline for its predicate,
// not a fresh timeout per unrelated item, and there are no sleeps or retries.
class Bus {
    items = [];
    #waiters = new Set();
    push(item) {
        this.items.push(item);
        for (const check of [...this.#waiters]) check();
    }
    wait(label, predicate, count = 1, timeout = 5000) {
        return new Promise((resolve, reject) => {
            const check = () => {
                const matches = this.items.filter(predicate);
                if (matches.length < count) return false;
                done();
                resolve(matches);
                return true;
            };
            const timer = setTimeout(() => { done(); reject(new Error(`Timed out waiting for ${label}`)); }, timeout);
            const done = () => { clearTimeout(timer); this.#waiters.delete(check); };
            if (!check()) this.#waiters.add(check);
        });
    }
}

// One host process per case: a fresh fixture, fresh cookies and a joined exit.
async function startHost(evidence) {
    const child = spawn(hostExecutable, ['-assets', assets], { stdio: ['ignore', 'pipe', 'inherit'], env: { ...process.env } });
    const exited = new Promise((resolve, reject) => {
        child.once('error', reject);
        child.once('exit', (code, signal) => resolve({ code, signal }));
    });
    exited.catch(() => {});
    const reports = new Bus();
    evidence.host = reports.items;
    const lines = createInterface({ input: child.stdout });
    lines.on('line', line => reports.push(JSON.parse(line)));
    let stopped;
    const host = {
        reports,
        async request(method, path, body) {
            const response = await fetch(host.origin + path, {
                method, signal: AbortSignal.timeout(5000),
                ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
            });
            return response;
        },
        async control(path, body) {
            const response = await host.request('POST', path, body);
            assert.equal(response.status, 204, `${path} failed: ${await response.text()}`);
        },
        publish(group, items) { return host.control('/fixture/publish', { group, items }); },
        async signals() { return (await host.request('GET', '/fixture/signals')).json(); },
        async wait(condition) {
            const response = await host.request('GET', `/fixture/wait?${condition}`);
            assert.equal(response.status, 204, `fixture condition ${condition}: ${await response.text()}`);
        },
        // SIGTERM must produce Arc's joined shutdown, never a forced kill.
        stop() {
            stopped ??= (async () => {
                child.kill('SIGTERM');
                const kill = setTimeout(() => child.kill('SIGKILL'), 7000);
                try {
                    const result = await exited;
                    assert.equal(result.code, 0, `Host did not join cleanly: ${JSON.stringify(result)}`);
                } finally {
                    clearTimeout(kill);
                    lines.close();
                }
                const joined = reports.items.find(report => report.joined);
                assert.ok(joined, 'Host must report completed Arc shutdown');
                assert.equal(joined.signals.open, joined.signals.close, 'All opened sources joined');
                return joined;
            })();
            return stopped;
        },
    };
    const [announcement] = await Promise.race([
        reports.wait('host announcement', report => report.origin),
        exited.then(result => { throw new Error(`Host exited before announcing: ${JSON.stringify(result)}`); }),
    ]);
    assert.match(announcement.origin, /^http:\/\/127\.0\.0\.1:\d+$/);
    host.origin = announcement.origin;
    // The listener exists, so one bounded request waits for Arc admission.
    const ready = await host.request('GET', '/fixture/ready');
    assert.equal(ready.status, 200);
    host.names = await ready.json();
    assert.ok(host.names.All, 'Required generated query is missing');
    return host;
}

async function openPage(host, query, evidence, label) {
    const context = await browser.newContext();
    const page = await context.newPage();
    const record = { label, console: [], errors: [], requests: [], sockets: [], renders: [] };
    evidence.pages.push(record);
    const requests = new Bus();
    const sockets = new Bus();
    const consoleMessages = new Bus();
    page.on('console', message => { const text = `${message.type()}: ${message.text()}`; record.console.push(text); consoleMessages.push(text); });
    page.on('pageerror', error => record.errors.push(String(error)));
    page.on('request', request => {
        record.requests.push({ method: request.method(), url: request.url(), type: request.resourceType(), body: request.postData() });
        requests.push(request);
    });
    page.on('websocket', socket => {
        const frames = { url: socket.url(), sent: [], received: [], closed: false };
        record.sockets.push(frames);
        socket.on('framesent', frame => frames.sent.push(String(frame.payload)));
        socket.on('framereceived', frame => frames.received.push(String(frame.payload)));
        socket.on('close', () => { frames.closed = true; sockets.push({ ...frames, event: 'close' }); });
        sockets.push({ socket, frames, event: 'open' });
    });
    const response = await page.goto(`${host.origin}/fixture/browser/${query}`, { timeout: 5000 });
    assert.equal(response.status(), 200);
    const state = async (label, predicate, argument, timeout = 5000) => {
        try {
            await page.waitForFunction(predicate, argument, { timeout });
        } catch (error) {
            throw new Error(`Page state '${label}' not reached: ${error.message}`, { cause: error });
        } finally {
            record.renders = await page.evaluate(() => window.__arcRenders ?? []).catch(() => record.renders);
        }
    };
    return { context, page, requests, sockets, consoleMessages, record, state };
}

// Waits for the hook-returned, rendered collection: titles in order, dates hydrated.
const rendered = ([titles]) => {
    const state = window.__arcState;
    const dom = [...document.querySelectorAll('#items li')].map(item => item.textContent);
    return state?.isReady && state.isSuccess && state.hydrated && state.titles.join('|') === titles && dom.join('|') === titles;
};
const items = (...titles) => titles.map((title, index) => ({ id: String.fromCharCode(97 + index), title, createdAt }));
const isHubRequest = suffix => request => request.method() === 'POST' && new URL(request.url()).pathname === hubSSE + suffix;
const isEventSource = request => request.resourceType() === 'eventsource' && new URL(request.url()).pathname === hubSSE;

// Runs one case with its own evidence; failure evidence is diagnostic, not a golden.
function browserTest(name, body) {
    test(name, async () => {
        const evidence = { name, pages: [] };
        const contexts = [];
        let host;
        try {
            host = await startHost(evidence);
            await body({ host, evidence, open: async (query, label) => {
                const opened = await openPage(host, query, evidence, label);
                contexts.push(opened.context);
                return opened;
            } });
            for (const context of contexts.splice(0)) await context.close();
            await host.stop();
        } catch (error) {
            evidence.error = String(error?.stack ?? error);
            failures.push(evidence);
            throw error;
        } finally {
            for (const context of contexts) await context.close().catch(() => {});
            await host?.stop().catch(() => {});
        }
    });
}

browserTest('default <Arc> uses the SSE hub with an HttpOnly cookie owner the page cannot read', async ({ host, open }) => {
    const owner = await open('?group=alpha', 'owner');
    await owner.state('baseline', rendered, ['alpha-old|alpha-gone']);
    const [subscribe] = await owner.requests.wait('subscribe POST', isHubRequest('/subscribe'));
    const body = subscribe.postDataJSON();
    assert.equal(body.request.queryName, host.names.All);
    assert.deepEqual(body.request.arguments, { group: 'alpha' });
    assert.ok(Number.isSafeInteger(body.revision) && body.revision > 0, 'Server advertised subscription revisions');
    const cookieName = cookiePrefix + body.connectionId;

    const cookies = (await owner.context.cookies(host.origin + hubSSE)).filter(cookie => cookie.name.startsWith(cookiePrefix));
    assert.equal(cookies.length, 1, 'one hub ownership cookie');
    assert.equal(cookies[0].name, cookieName, 'cookie names the hub connection');
    assert.notEqual(cookies[0].value, body.connectionId, 'ownership evidence is not the connection ID');
    assert.equal(cookies[0].httpOnly, true);
    assert.equal(cookies[0].sameSite, 'Strict');
    assert.equal(cookies[0].path, hubSSE);
    assert.equal(cookies[0].secure, false, 'plain-HTTP loopback cookie');
    assert.equal((await owner.context.cookies(`${host.origin}/fixture/browser/`)).filter(cookie => cookie.name.startsWith(cookiePrefix)).length, 0, 'owner is not sent with page assets');
    assert.ok(!(await owner.page.evaluate(() => document.cookie)).includes(cookiePrefix), 'page script cannot read the owner');
    // The browser, not the client library, attached the owner to the control POST.
    assert.match((await subscribe.allHeaders()).cookie ?? '', new RegExp(`(?:^|; )${cookieName}=`));
    assert.equal(owner.requests.items.filter(isEventSource).length, 1, 'one EventSource hub connection');
    assert.equal(owner.record.sockets.length, 0, 'default <Arc> opened no WebSocket');

    await host.publish('alpha', items('alpha-new', 'alpha-added'));
    await owner.state('delta update', rendered, ['alpha-new|alpha-added']);

    // A separate browser profile on the same origin knows the connection and query
    // IDs but not the HttpOnly owner: Arc refuses both controls without effects.
    const foreign = await open('', 'foreign');
    await foreign.state('foreign baseline', rendered, ['alpha-new|alpha-added']);
    const before = await host.signals();
    const statuses = await foreign.page.evaluate(async ([hub, control]) => {
        const post = async (path, value) => (await fetch(hub + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) })).status;
        return [await post('/subscribe', { ...control, queryId: 'foreign', revision: control.revision + 1 }), await post('/unsubscribe', { connectionId: control.connectionId, queryId: control.queryId, revision: control.revision })];
    }, [hubSSE, body]);
    assert.deepEqual(statuses, [404, 404], 'foreign owner controls are refused as unknown');
    assert.deepEqual(await host.signals(), before, 'refused controls changed no lifecycle');
    await host.publish('alpha', items('alpha-owned'));
    await owner.state('owner still subscribed', rendered, ['alpha-owned']);
});

browserTest('<Arc> WebSocket hub delivers through one socket without a hub cookie', async ({ host, open }) => {
    const client = await open('?transport=ws&group=beta', 'websocket');
    await client.state('baseline', rendered, ['beta-old|beta-gone']);
    const [opened] = await client.sockets.wait('hub WebSocket', item => item.event === 'open');
    assert.equal(new URL(opened.frames.url).pathname, hubWS);
    const sent = opened.frames.sent.map(frame => JSON.parse(frame));
    const received = opened.frames.received.map(frame => JSON.parse(frame));
    const subscribe = sent.findLast(frame => frame.type === 'Subscribe');
    assert.equal(subscribe?.payload?.queryName, host.names.All);
    assert.deepEqual(subscribe.payload.arguments, { group: 'beta' });
    assert.ok(Number.isSafeInteger(subscribe.revision) && subscribe.revision > 0, 'Subscribe carries the negotiated revision');
    assert.ok(received.some(frame => frame.type === 'Connected'), 'Connected arrived on the socket');
    assert.ok(received.some(frame => frame.type === 'QueryResult' && frame.queryId === subscribe.queryId), 'result arrived for the subscription');

    await host.publish('beta', items('beta-new'));
    await client.state('delta update', rendered, ['beta-new']);
    assert.equal(client.record.sockets.length, 1, 'one hub WebSocket');
    assert.equal(client.requests.items.filter(isEventSource).length, 0, 'no SSE hub was opened');
    assert.equal((await client.context.cookies()).filter(cookie => cookie.name.startsWith(cookiePrefix)).length, 0, 'WebSocket ownership needs no cookie');
    const signals = await host.signals();
    assert.equal(signals.open - signals.close, 1, `exactly one live source: ${JSON.stringify(signals)}`);
});

for (const retention of [undefined, 0]) {
    const label = retention === undefined ? 'default cache retention' : 'immediate cache eviction';
    browserTest(`StrictMode double mount keeps one live subscription (${label})`, async ({ host, open }) => {
        const client = await open(`?strict=1&group=alpha${retention === undefined ? '' : `&retention=${retention}`}`, 'strict');
        await client.state('baseline', rendered, ['alpha-old|alpha-gone']);
        await host.publish('alpha', items('alpha-strict'));
        await client.state('delta update', rendered, ['alpha-strict']);
        const signals = await host.signals();
        assert.equal(signals.open - signals.close, 1, `exactly one live source after the double mount: ${JSON.stringify(signals)}`);
        assert.equal(client.requests.items.filter(isEventSource).length, 1, 'one hub connection after the double mount');
        assert.equal(await client.page.evaluate(() => document.querySelectorAll('#items li').length), 1, 'no duplicated rendered items');
        if (retention === 0) {
            await client.page.evaluate(() => window.__unmount());
            // A real unmount releases the only subscription; the server joins its source.
            await host.wait('active=0');
            const final = await host.signals();
            assert.equal(final.open, final.close, 'unmount joined every source');
            const subscribes = client.requests.items.filter(isHubRequest('/subscribe')).length;
            const unsubscribes = client.requests.items.filter(isHubRequest('/unsubscribe')).length;
            assert.equal(subscribes, final.open, 'every subscribe POST opened exactly one source');
            assert.ok(unsubscribes <= subscribes, 'no unsubscribe without a subscription');
        }
    });
}

browserTest('the SSE hub reconnects after a real server close and resubscribes with a fresh owner', async ({ host, open }) => {
    const client = await open('?group=alpha', 'reconnect');
    await client.state('baseline', rendered, ['alpha-old|alpha-gone']);
    await host.publish('alpha', items('alpha-before-close'));
    await client.state('first generation update', rendered, ['alpha-before-close']);
    const [first] = await client.requests.wait('first subscribe', isHubRequest('/subscribe'));

    // The host joins the current Arc generation and serves a new one on the same
    // origin. The page receives no hint: only EventSource failure drives recovery.
    await host.control('/fixture/shutdown');
    const [restarted] = await host.reports.wait('restart report', report => report.restarted === 1);
    assert.equal(restarted.signals.open, 1);
    assert.equal(restarted.signals.close, 1, 'server close joined the first generation');
    await client.consoleMessages.wait('EventSource error', text => text.includes('SSE hub connection error'));

    // The ReconnectPolicy's first back-off is one second; the fresh generation's
    // baseline replaces the pre-close update.
    await client.state('fresh baseline after reconnect', rendered, ['alpha-old|alpha-gone'], 10000);
    const subscribes = await client.requests.wait('resubscribe', isHubRequest('/subscribe'), 2);
    const second = subscribes.at(-1).postDataJSON();
    assert.notEqual(second.connectionId, first.postDataJSON().connectionId, 'reconnect negotiated a new hub connection');
    assert.equal(second.request.queryName, host.names.All);
    assert.equal(client.requests.items.filter(isEventSource).length, 2, 'one EventSource per generation');
    const cookies = await client.context.cookies(host.origin + hubSSE);
    assert.ok(cookies.some(cookie => cookie.name === cookiePrefix + second.connectionId && cookie.httpOnly), 'new generation issued a new HttpOnly owner');

    await host.publish('alpha', items('alpha-after-restart'));
    await client.state('second generation update', rendered, ['alpha-after-restart']);
});

browserTest('terminal Unauthorized settles the hook and never resubscribes', async ({ host, open }) => {
    const client = await open('?group=alpha', 'unauthorized');
    await client.state('baseline', rendered, ['alpha-old|alpha-gone']);
    await host.control('/fixture/deny');
    await host.publish('alpha', items('alpha-denied'));
    await client.state('unauthorized', () => {
        const state = window.__arcState;
        return state?.isReady && state.isAuthorized === false && !state.titles.includes('alpha-denied') && document.querySelector('section')?.dataset.authorized === 'false';
    });
    // The server terminated and joined the source; the denied value never rendered.
    await host.wait('event=close&count=1');
    const renders = await client.page.evaluate(() => window.__arcRenders);
    assert.ok(!renders.some(render => render.titles.includes('alpha-denied')), 'denied update reached the page');
    await host.publish('alpha', items('alpha-later'));
    await client.context.close();
    const joined = await host.stop();
    assert.equal(joined.signals.open, 1, 'Unauthorized is terminal: the client did not resubscribe');
    assert.equal(client.requests.items.filter(isHubRequest('/subscribe')).length, 1, 'one subscribe POST in total');
});

browserTest('joined host shutdown ends live SSE and WebSocket hubs in the browser', async ({ host, open }) => {
    const sse = await open('?group=alpha', 'shutdown-sse');
    const ws = await open('?transport=ws&group=beta', 'shutdown-ws');
    await sse.state('SSE baseline', rendered, ['alpha-old|alpha-gone']);
    await ws.state('WebSocket baseline', rendered, ['beta-old|beta-gone']);
    await host.wait('active=2');
    // Cumulative opens include the WebSocket client's pre-negotiation Subscribe
    // and its revisioned replacement; two sources are live at shutdown.
    const live = await host.signals();
    assert.equal(live.open - live.close, 2, `two live sources before shutdown: ${JSON.stringify(live)}`);

    const sseClosed = sse.consoleMessages.wait('SSE hub error after shutdown', text => text.includes('SSE hub connection error'));
    const wsClosed = ws.sockets.wait('WebSocket close after shutdown', item => item.event === 'close');
    const joined = await host.stop();
    assert.equal(joined.generation, 1);
    assert.equal(joined.signals.open, live.open, 'no source opened during shutdown');
    assert.equal(joined.signals.close, live.open, 'shutdown joined both live sources');
    assert.equal(joined.signals.dispose, joined.signals.factory, 'every resolved resource was disposed');
    // The browser observes the server-side close on both transports.
    await Promise.all([sseClosed, wsClosed]);
});
