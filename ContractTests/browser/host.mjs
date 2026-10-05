// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';

// Items arrive in order. Each wait has one absolute deadline for its predicate,
// not a fresh timeout per unrelated item, and there are no sleeps or retries.
export class Bus {
    items = [];
    #waiters = new Set();
    push(item) {
        this.items.push(item);
        for (const check of [...this.#waiters]) check();
    }
    wait(label, predicate, count = 1, timeout = 5000, signal) {
        return new Promise((resolve, reject) => {
            const check = () => {
                const matches = this.items.filter(predicate);
                if (matches.length < count) return false;
                done();
                resolve(matches);
                return true;
            };
            const abort = () => { done(); reject(signal.reason); };
            const timer = setTimeout(() => { done(); reject(new Error(`Timed out waiting for ${label}`)); }, timeout);
            const done = () => { clearTimeout(timer); this.#waiters.delete(check); signal?.removeEventListener('abort', abort); };
            if (signal?.aborted) abort();
            else if (!check()) {
                this.#waiters.add(check);
                signal?.addEventListener('abort', abort, { once: true });
            }
        });
    }
}

// One host process per case: a fresh fixture, fresh cookies and a joined exit.
// Command/time budgets can be supplied by the startup-failure regression tests.
export async function startHost(evidence, {
    executable = process.env.ARC_BROWSER_HOST,
    args = ['-assets', process.env.ARC_BROWSER_ASSETS],
    startupTimeout = 5000,
    requestTimeout = 5000,
    stopTimeout = 7000,
} = {}) {
    const child = spawn(executable, args, { stdio: ['ignore', 'pipe', 'inherit'], env: { ...process.env } });
    const exited = new Promise((resolve, reject) => {
        child.once('error', reject);
        // 'exit' can precede the final stdout report; 'close' joins stdio too.
        child.once('close', (code, signal) => resolve({ code, signal }));
    });
    exited.catch(() => {});
    const reports = new Bus();
    evidence.host = reports.items;
    const lines = createInterface({ input: child.stdout });
    lines.on('line', line => reports.push(JSON.parse(line)));
    let terminated;
    const terminate = () => {
        terminated ??= (async () => {
            child.kill('SIGTERM');
            const kill = setTimeout(() => child.kill('SIGKILL'), stopTimeout);
            try {
                return await exited;
            } finally {
                clearTimeout(kill);
                lines.close();
            }
        })();
        return terminated;
    };
    let stopped;
    const host = {
        reports,
        async request(method, path, body) {
            const response = await fetch(host.origin + path, {
                method, signal: AbortSignal.timeout(requestTimeout),
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
        // Successful cases require Arc's joined shutdown, never a forced kill.
        stop() {
            stopped ??= (async () => {
                const result = await terminate();
                assert.equal(result.code, 0, `Host did not join cleanly: ${JSON.stringify(result)}`);
                const joined = reports.items.find(report => report.joined);
                assert.ok(joined, 'Host must report completed Arc shutdown');
                assert.equal(joined.signals.open, joined.signals.close, 'All opened sources joined');
                return joined;
            })();
            return stopped;
        },
    };
    const startup = new AbortController();
    try {
        const [announcement] = await Promise.race([
            reports.wait('host announcement', report => report.origin, 1, startupTimeout, startup.signal),
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
    } catch (error) {
        // browserTest has no host handle yet. Join the child here even when it
        // never announces, refuses readiness, or ignores graceful termination.
        await terminate().catch(() => {}); // A spawn error is already the startup error.
        throw error;
    } finally {
        startup.abort();
    }
}
