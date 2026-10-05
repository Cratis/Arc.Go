// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { startHost } from './host.mjs';

// Real subprocesses and a real loopback readiness endpoint: no mocked kill or
// exit event can accidentally make an orphan look joined.
const fixture = `
    const { createServer } = require('node:http');
    const mode = process.argv[1];
    console.log(JSON.stringify({ pid: process.pid }));
    process.on('SIGTERM', () => {
        console.log(JSON.stringify({ terminated: true }));
        if (mode === 'ignores-term') return;
        console.log(JSON.stringify({ joined: true, signals: { open: 0, close: 0 } }));
        process.exit(0);
    });
    if (mode === 'exits') process.exit(3);
    const server = createServer((request, response) => {
        if (mode === 'readiness-timeout') return;
        response.statusCode = mode === 'readiness-status' ? 503 : 200;
        response.end(mode === 'missing-name' ? '{}' : JSON.stringify({ All: 'fixture.All' }));
    });
    server.listen(0, '127.0.0.1', () => {
        if (mode === 'no-announcement' || mode === 'ignores-term') return;
        console.log(JSON.stringify({ origin: mode === 'invalid-origin' ? 'http://example.com' : 'http://127.0.0.1:' + server.address().port }));
    });
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
