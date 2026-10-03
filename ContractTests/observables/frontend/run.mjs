// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';

const directory = import.meta.dirname;
const root = resolve(directory, '../../..');
const output = join(root, '.ai-work/observable-client');
const executable = join(output, 'fixturehost');
assert.equal(process.version, 'v26.8.1', 'Required pinned Node is missing');
const env = { ...process.env, GOWORK: 'off', GOTOOLCHAIN: 'local' };

// Every producer is awaited and its exit preserved. There is no piped/filter-only
// success check, optional tool skip, detached go-run process, or golden capture.
async function stage(command, args, cwd, timeout = 120000) {
    console.log(`Stage: ${command} ${args.join(' ')}`);
    const child = spawn(command, args, { cwd, env, stdio: 'inherit' });
    const result = new Promise((resolve, reject) => {
        child.once('error', reject);
        child.once('exit', (code, signal) => resolve({ code, signal }));
    });
    const stop = () => child.kill('SIGTERM');
    process.on('SIGINT', stop);
    process.on('SIGTERM', stop);
    const timer = setTimeout(() => child.kill('SIGKILL'), timeout);
    try {
        const { code, signal } = await result;
        assert.equal(code, 0, `${command} failed: exit=${code} signal=${signal}`);
    } finally {
        clearTimeout(timer);
        process.off('SIGINT', stop);
        process.off('SIGTERM', stop);
    }
}
async function runtime() {
    const host = spawn(executable, [], { cwd: root, env, stdio: ['ignore', 'pipe', 'inherit'] });
    const joined = new Promise((resolve, reject) => {
        host.once('error', reject);
        host.once('exit', (code, signal) => resolve({ code, signal }));
    });
    // Attach a rejection handler immediately, even if spawn fails before readiness.
    joined.catch(() => {});
    const stop = () => host.kill('SIGTERM');
    process.on('SIGINT', stop);
    process.on('SIGTERM', stop);
    const lines = createInterface({ input: host.stdout });
    let timer;
    try {
        const announcement = new Promise((resolve, reject) => {
            lines.once('line', line => { try { resolve(JSON.parse(line)); } catch (error) { reject(error); } });
            timer = setTimeout(() => reject(new Error('Fixture announcement timed out')), 5000);
        });
        const address = await Promise.race([announcement, joined.then(result => { throw new Error(`Fixture exited before readiness: ${JSON.stringify(result)}`); })]);
        clearTimeout(timer);
        assert.match(address.origin, /^http:\/\/127\.0\.0\.1:\d+$/);
        // Listener already exists, so one bounded request can wait for Serve's
        // startup. A listener announcement alone is never accepted as readiness.
        const ready = await fetch(address.origin + '/fixture/ready', { signal: AbortSignal.timeout(5000) });
        assert.equal(ready.status, 200);
        const names = await ready.json();
        assert.ok(names.All, 'Required registered query is missing');
        env.ARC_FIXTURE_ORIGIN = address.origin;
        env.ARC_FIXTURE_QUERY = names.All;
        await stage(process.execPath, ['--test', '--test-timeout=45000', 'client.test.mjs'], directory, 60000);
    } finally {
        clearTimeout(timer);
        lines.close();
        host.kill('SIGTERM');
        const kill = setTimeout(() => host.kill('SIGKILL'), 7000);
        try {
            const result = await joined;
            assert.equal(result.code, 0, `Fixture did not join cleanly: ${JSON.stringify(result)}`);
        } finally {
            clearTimeout(kill);
            process.off('SIGINT', stop);
            process.off('SIGTERM', stop);
        }
    }
}
const selected = process.argv.slice(2);
assert.ok(selected.length <= 1 && (!selected.length || ['install', 'compile', 'fixture-build', 'runtime'].includes(selected[0])), 'Unknown stage');
const stages = selected.length ? selected : ['install', 'compile', 'fixture-build', 'runtime'];
for (const name of stages) {
    if (name === 'install') {
        await stage('npm', ['--version'], directory);
        // npm itself enforces the patch through engine-strict during npm ci.
        await stage('npm', ['ci', '--engine-strict'], directory);
    } else if (name === 'compile') await stage('npm', ['run', 'compile'], directory);
    else if (name === 'fixture-build') {
        await mkdir(output, { recursive: true });
        await stage('go', ['build', '-o', executable, './ContractTests/observables/fixturehost'], root);
    } else await runtime();
}
