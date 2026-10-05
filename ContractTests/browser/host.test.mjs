// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { once } from 'node:events';
import { syncBuiltinESMExports } from 'node:module';
import { test } from 'node:test';
import { startHost } from './host.mjs';

// Real subprocesses and a real loopback readiness endpoint: no mocked kill or
// exit event can accidentally make an orphan look joined.
// This descendant owns the inherited stdout until the test releases its final
// report. The host process can exit first without relying on scheduler timing.
const delayedReportWriter = `
    const { createServer } = require('node:http');
    const server = createServer((request, response) => {
        response.end(() => {
            server.close();
            process.stdout.write(JSON.stringify({ joined: true, signals: { open: 0, close: 0 } }) + '\\n', () => process.exit(0));
        });
    });
    server.listen(0, '127.0.0.1', () => {
        console.log(JSON.stringify({ writerOrigin: 'http://127.0.0.1:' + server.address().port, writerPID: process.pid }));
        process.send('ready');
    });
`;

const fixture = `
    const { createServer } = require('node:http');
    const mode = process.argv[1];
    console.log(JSON.stringify({ pid: process.pid }));
    process.on('SIGTERM', () => {
        console.log(JSON.stringify({ terminated: true }));
        if (mode === 'ignores-term') return;
        if (mode === 'delayed-report') process.exit(0);
        console.log(JSON.stringify({ joined: true, signals: { open: 0, close: 0 } }));
        process.exit(0);
    });
    if (mode === 'exits') process.exit(3);
    const server = createServer((request, response) => {
        if (mode === 'readiness-timeout') return;
        response.statusCode = mode === 'readiness-status' ? 503 : 200;
        response.end(mode === 'missing-name' ? '{}' : JSON.stringify({ All: 'fixture.All' }));
    });
    const listen = () => server.listen(0, '127.0.0.1', () => {
        if (mode === 'no-announcement' || mode === 'ignores-term') return;
        console.log(JSON.stringify({ origin: mode === 'invalid-origin' ? 'http://example.com' : 'http://127.0.0.1:' + server.address().port }));
    });
    if (mode === 'delayed-report') {
        const { spawn } = require('node:child_process');
        const writer = spawn(process.execPath, ['-e', ${JSON.stringify(delayedReportWriter)}], { stdio: ['ignore', 'inherit', 'inherit', 'ipc'] });
        writer.once('message', listen);
    } else listen();
`;

const options = mode => ({
    executable: process.execPath,
    args: ['-e', fixture, mode],
    startupTimeout: 1000,
    requestTimeout: 100,
    stopTimeout: 100,
});

function assertExited(evidence) {
    const pid = evidence.host.find(report => report.pid)?.pid;
    assert.ok(pid, 'fixture announced its PID');
    assert.throws(() => process.kill(pid, 0), { code: 'ESRCH' }, 'startup rejection must join the child before returning');
}

for (const [mode, error] of [
    ['no-announcement', /Timed out waiting for host announcement/],
    ['invalid-origin', /did not match/],
    ['readiness-timeout', /timeout/i],
    ['readiness-status', /503/],
    ['missing-name', /Required generated query is missing/],
    ['ignores-term', /Timed out waiting for host announcement/],
    ['exits', /Host exited before announcing/],
]) {
    test(`startup failure joins the fixture (${mode})`, async t => {
        const evidence = {};
        // Safety fallback makes a broken cleanup regression fail without leaving
        // its own subprocess behind. The assertion runs before this fallback.
        t.after(() => {
            const pid = evidence.host?.find(report => report.pid)?.pid;
            if (pid) {
                try { process.kill(pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
            }
        });
        await assert.rejects(startHost(evidence, options(mode)), error);
        assertExited(evidence);
        if (mode !== 'exits') assert.ok(evidence.host.some(report => report.terminated), 'SIGTERM attempted before joining');
    });
}

test('a spawn failure preserves the original startup error', async () => {
    await assert.rejects(startHost({}, { ...options('unused'), executable: '/nonexistent/arc-browser-fixture' }), { code: 'ENOENT' });
});

test('shutdown drains the final report after process exit before closing readline', async t => {
    const spawn = childProcess.spawn;
    let child;
    // Observe the real process without replacing its streams or lifecycle events.
    t.mock.method(childProcess, 'spawn', (...args) => {
        child = spawn(...args);
        return child;
    });
    syncBuiltinESMExports();
    t.after(() => {
        t.mock.restoreAll();
        syncBuiltinESMExports();
    });
    const host = await startHost({}, options('delayed-report'));
    const [writer] = await host.reports.wait('stdout writer', report => report.writerOrigin);
    t.after(() => {
        try { process.kill(writer.writerPID, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
    });
    const exit = once(child, 'exit');
    const stopping = host.stop();
    stopping.catch(() => {});
    await exit;
    // The real child has exited but its inherited stdout is still open. Only
    // now may the writer emit the final report and finish the stream.
    const response = await fetch(writer.writerOrigin, { signal: AbortSignal.timeout(1000) });
    assert.equal(response.status, 200);
    assert.equal((await stopping).joined, true);
    assert.equal(child.stdout.readableEnded, true, 'stop joins stdout as well as the process');
});

test('a ready host retains strict joined shutdown and idempotent stop', async () => {
    const evidence = {};
    const host = await startHost(evidence, options('ready'));
    try {
        assert.equal(host.names.All, 'fixture.All');
    } finally {
        const stopping = host.stop();
        assert.equal(host.stop(), stopping);
        assert.equal((await stopping).joined, true);
    }
    assertExited(evidence);
});
