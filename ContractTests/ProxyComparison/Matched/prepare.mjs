// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { directory, run } from '../helpers.mjs';

export async function prepare() {
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
    await writeFile(join(consumer, 'input.go'), await readFile(join(directory, 'Matched/input.go.txt')));
    await writeFile(join(consumer, 'profile.json'), JSON.stringify({ formatVersion: 1, name: 'matched',
        clientHttp: { 'ProxyComparison.Listing.All': 'Get' }, typescript: { out: 'web' } }));
    // Observe completes the C# source: identity-less Listing, like C#'s JSON fallback.
    await writeFile(join(consumer, 'observable.go'), await readFile(join(directory, 'Matched/observable.go.txt')));
    return { consumer, tools, go: process.env.GO || 'go' };
}

export function tidyConsumer({ consumer, tools, go }) {
    const env = { ...process.env, GOWORK: 'off', GOTOOLCHAIN: 'local', GOPROXY: 'off', GONOPROXY: 'none', GOFLAGS: '-mod=mod' };
    const format = '{{if not .Main}}{{.Path}} {{.Version}}{{if .Replace}} => {{.Replace.Path}} {{.Replace.Version}}{{end}}{{end}}';
    const graph = cwd => run(go, ['list', '-m', '-f', format, 'all'], cwd, { ...env, GOFLAGS: '-mod=readonly' })
        .split('\n').map(line => line.trim()).filter(Boolean);
    const pinned = new Set(graph(tools));
    // Offline resolution alone could admit an unrelated dependency already in the cache.
    run(go, ['mod', 'tidy'], consumer, env);
    for (const module of graph(consumer)) {
        assert.ok(pinned.has(module), `Consumer module is outside the pinned tools graph: ${module}`);
    }
}

export async function generate() {
    const prepared = await prepare();
    const { consumer, tools, go } = prepared;
    const env = { ...process.env, GOWORK: 'off', GOTOOLCHAIN: 'local' };
    // Populate only the pinned tools graph before offline consumer resolution.
    run(go, ['mod', 'download'], tools, env);
    run(go, ['run', './cmd/arc-gen', '-dir', consumer, '-config', join(consumer, 'profile.json'), '.'], tools, env);
    // Generated adapters may import more runtime packages, but never another module or version.
    tidyConsumer(prepared);
    // -check is production CLI verification, not a comparison with hand-written Go output.
    run(go, ['run', './cmd/arc-gen', '-dir', consumer, '-config', join(consumer, 'profile.json'), '-check', '.'], tools, env);
    return join(consumer, 'web');
}

if (process.argv[1] && resolve(process.argv[1]) === import.meta.filename) console.log(await generate());
