// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { copyFile, mkdir, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';

const directory = import.meta.dirname;
const root = resolve(directory, '../..');
const output = join(root, '.ai-work/browser-contract');
const assets = join(output, 'assets');
const host = join(output, 'host');
assert.equal(process.version, 'v26.8.1', 'Required pinned Node is missing');
const env = { ...process.env, GOWORK: 'off', GOTOOLCHAIN: 'local', ARC_BROWSER_ASSETS: assets, ARC_BROWSER_HOST: host };

// Every producer is awaited and its exit preserved. There is no optional skip:
// a missing browser, host or bundle fails the stage that needs it.
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

// Bare imports from the production-generated proxies outside this package
// resolve from THIS locked installation, so the page has one Arc and one React.
const lockedRuntime = {
    name: 'one-locked-runtime',
    setup(builder) {
        builder.onResolve({ filter: /^(?:@cratis\/|react(?:-dom)?(?:\/|$))/ }, async args => {
            if (args.pluginData?.locked) return undefined;
            const resolved = await builder.resolve(args.path, { kind: args.kind, resolveDir: directory, pluginData: { locked: true } });
            if (resolved.errors.length) return { errors: resolved.errors };
            return { path: resolved.path };
        });
    },
};

async function bundle() {
    const { build } = await import('esbuild');
    await rm(assets, { recursive: true, force: true });
    await mkdir(assets, { recursive: true });
    // Development React is required: StrictMode double-invokes effects only there.
    const result = await build({
        entryPoints: [join(directory, 'app.jsx')], outfile: join(assets, 'app.js'),
        bundle: true, format: 'esm', platform: 'browser', target: 'es2022', jsx: 'automatic', keepNames: true,
        define: { 'process.env.NODE_ENV': '"development"' }, logLevel: 'warning', plugins: [lockedRuntime],
    });
    assert.equal(result.errors.length, 0, 'Bundle failed');
    await copyFile(join(directory, 'index.html'), join(assets, 'index.html'));
}

const known = ['install', 'bundle', 'host-build', 'runtime'];
const selected = process.argv.slice(2);
assert.ok(selected.length <= 1 && (!selected.length || known.includes(selected[0])), 'Unknown stage');
for (const name of selected.length ? selected : known) {
    if (name === 'install') {
        await stage('npm', ['--version'], directory);
        // npm itself enforces the patch through engine-strict during npm ci.
        await stage('npm', ['ci', '--engine-strict'], directory);
    } else if (name === 'bundle') {
        console.log('Stage: bundle app.jsx');
        await bundle();
    } else if (name === 'host-build') {
        await mkdir(output, { recursive: true });
        await stage('go', ['build', '-o', host, './ContractTests/browser/host'], root);
    } else {
        await stage(process.execPath, ['--test', '--test-concurrency=1', '--test-timeout=60000', 'host.test.mjs', 'browser.test.mjs'], directory, 240000);
    }
}
