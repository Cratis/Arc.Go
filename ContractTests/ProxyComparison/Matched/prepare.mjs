// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { performance } from 'node:perf_hooks';
import { directory, run } from '../helpers.mjs';

export async function prepare(baseEnv = process.env) {
    const env = { ...baseEnv, GOWORK: 'off', GOTOOLCHAIN: 'local' };
    const root = resolve(directory, '../..');
    const scratch = process.env.AI_WORK_OUTPUT || join(root, '.ai-work/proxy-comparison');
    await mkdir(scratch, { recursive: true });
    const consumer = await mkdtemp(join(scratch, 'matched-'));
    const tools = join(root, 'tools');
    // Independent consumption of the existing tools module's fetchable runtime pin.
    // No workspace, local replace, extra published module or runtime dependency.
    await writeFile(join(consumer, 'go.mod'), (await readFile(join(tools, 'go.mod'), 'utf8'))
        .replace('module github.com/cratis/arc.go/tools', 'module example.test/proxy-comparison'));
    await writeFile(join(consumer, 'go.sum'), await readFile(join(tools, 'go.sum')));
    // Full-graph downloads add otherwise-unused checksums; keep them in scratch files.
    await writeFile(join(consumer, 'download.mod'), await readFile(join(tools, 'go.mod')));
    await writeFile(join(consumer, 'download.sum'), await readFile(join(tools, 'go.sum')));
    await writeFile(join(consumer, 'input.go'), await readFile(join(directory, 'Matched/input.go.txt')));
    await writeFile(join(consumer, 'profile.json'), JSON.stringify({ formatVersion: 1, name: 'matched',
        clientHttp: { 'ProxyComparison.Listing.All': 'Get' }, typescript: { out: 'web' } }));
    // Observe completes the C# source: identity-less Listing, like C#'s JSON fallback.
    await writeFile(join(consumer, 'observable.go'), await readFile(join(directory, 'Matched/observable.go.txt')));
    const runtime = join(consumer, 'runtime-baseline');
    await mkdir(runtime);
    await writeFile(join(runtime, 'go.mod'), (await readFile(join(consumer, 'go.mod'), 'utf8'))
        .replace('module example.test/proxy-comparison', 'module example.test/runtime-baseline'));
    await writeFile(join(runtime, 'go.sum'), await readFile(join(consumer, 'go.sum')));
    await writeFile(join(runtime, 'input.go'), await readFile(join(consumer, 'input.go')));
    await writeFile(join(runtime, 'observable.go'), await readFile(join(consumer, 'observable.go')));
    // Include the adapter's runtime entry point while preserving the authored input's pins.
    await writeFile(join(runtime, 'runtime.go'), 'package consumer\nimport _ "github.com/cratis/arc.go"\n');
    return { consumer, tools, runtime, go: env.GO || 'go', env };
}

export function tidyConsumer({ consumer, runtime, go, env: baseEnv }, remainingTimeout = () => 60000) {
    const env = { ...baseEnv, GOPROXY: 'off', GONOPROXY: 'none', GOFLAGS: '-mod=mod' };
    const format = '{{if not .Main}}{{.Path}} {{.Version}}{{if .Replace}} => {{.Replace.Path}} {{.Replace.Version}}{{end}}{{end}}';
    const graph = cwd => run(go, ['list', '-m', '-f', format, 'all'], cwd, { ...env, GOFLAGS: '-mod=readonly' }, remainingTimeout())
        .split('\n').map(line => line.trim()).filter(Boolean);
    // Tidy the authored-input/runtime baseline, dropping arc-gen-only dependencies.
    run(go, ['mod', 'tidy'], runtime, env, remainingTimeout());
    const pinned = new Set(graph(runtime));
    // Offline resolution alone could admit an unrelated dependency already in the cache.
    run(go, ['mod', 'tidy'], consumer, env, remainingTimeout());
    for (const module of graph(consumer)) {
        assert.ok(pinned.has(module), `Consumer module is outside the pinned runtime graph: ${module}`);
    }
}

export async function generate(timeout, baseEnv = process.env) {
    assert.ok(timeout === undefined || (Number.isSafeInteger(timeout) && timeout > 0), 'Generation timeout must be a positive integer in milliseconds');
    const deadline = timeout === undefined ? undefined : performance.now() + timeout;
    // A single cold-cache budget prevents sequential steps from outliving the outer process.
    // Ordinary invocations retain the helper's 60s per-step timeout.
    const remainingTimeout = () => {
        if (deadline === undefined) return 60000;
        const remaining = Math.floor(deadline - performance.now());
        assert.ok(remaining > 0, 'Proxy generation deadline exceeded before starting the next Go step');
        return remaining;
    };
    const prepared = await prepare(baseEnv);
    const { consumer, tools, go, env } = prepared;
    // Download the full pinned graph, including runtime packages arc-gen does not import.
    run(go, ['mod', 'download', '-modfile', join(consumer, 'download.mod'), 'all'], tools, env, remainingTimeout());
    run(go, ['run', './cmd/arc-gen', '-dir', consumer, '-config', join(consumer, 'profile.json'), '.'], tools, env, remainingTimeout());
    // Generated adapters may import more runtime packages, but never another module or version.
    tidyConsumer(prepared, remainingTimeout);
    // -check is production CLI verification, not a comparison with hand-written Go output.
    run(go, ['run', './cmd/arc-gen', '-dir', consumer, '-config', join(consumer, 'profile.json'), '-check', '.'], tools, env, remainingTimeout());
    return join(consumer, 'web');
}

if (process.argv[1] && resolve(process.argv[1]) === import.meta.filename) {
    const timeout = process.env.ARC_PROXY_COMPARISON_TIMEOUT_MS;
    console.log(await generate(timeout === undefined ? undefined : Number(timeout)));
}
