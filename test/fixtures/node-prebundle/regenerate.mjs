import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(fileURLToPath(import.meta.url));
assert(process.argv.length === 2 || (process.argv.length === 3 && process.argv[2] === '--check'));
const check = process.argv[2] === '--check';
const tool = join(root, 'node_modules/.bin/esbuild');
const version = spawnSync(tool, ['--version'], { encoding: 'utf8', timeout: 30000 });
assert.equal(version.status, 0, version.stderr);
assert.equal(version.stdout.trim(), '0.25.12');
const flags = ['--bundle', '--packages=bundle', '--platform=node', '--target=node26',
  '--format=cjs', '--external:orva', '--external:orva/*', '--external:node:*', '--sourcemap=external'];
const sources = ['src/handler.js', 'src/message.js', 'src/handler.ts', 'src/message.ts'];
const probes = ['probes/asset.js', 'probes/asset.txt', 'probes/dynamic.js',
  'probes/native.js', 'probes/addon.node'];
const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const stage = mkdtempSync(join(tmpdir(), 'orva-prebundle-'));
const hashes = {};
function emit(path, bytes) {
  hashes[path] = sha(bytes);
  if (check) assert.deepEqual(readFileSync(join(root, path)), bytes, `stale ${path}`);
  else {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), bytes);
  }
}
function build(input, output) {
  return spawnSync(tool, [input, ...flags, `--outfile=${output}`],
    { cwd: stage, encoding: 'utf8', timeout: 30000 });
}
try {
  mkdirSync(join(stage, 'src'));
  mkdirSync(join(stage, 'probes'));
  for (const path of [...sources, ...probes]) copyFileSync(join(root, path), join(stage, path));
  for (const language of ['js', 'ts']) {
    const output = `${language}/upload/dist/handler.js`;
    const result = build(`src/handler.${language}`, output);
    assert.equal(result.status, 0, result.stderr);
    const code = readFileSync(join(stage, output));
    assert.match(code.toString(), /require\(["']orva["']\)/);
    assert.match(code.toString(), /require\(["']node:path["']\)/);
    emit(output, code);
    emit(`${output}.map`, readFileSync(join(stage, `${output}.map`)));
  }
  // Build-only probes: success cannot establish that runtime assets were shipped.
  assert.equal(build('probes/asset.js', 'probe-output/asset.js').status, 0);
  assert.match(readFileSync(join(stage, 'probe-output/asset.js'), 'utf8'), /asset\.txt/);
  assert.equal(build('probes/dynamic.js', 'probe-output/dynamic.js').status, 0);
  assert.match(readFileSync(join(stage, 'probe-output/dynamic.js'), 'utf8'), /require\(name\)/);
  const native = build('probes/native.js', 'probe-output/native.js');
  assert.notEqual(native.status, 0);
  assert.match(native.stderr, /No loader is configured for ".node"/);
  for (const path of [...sources, ...probes, 'regenerate.mjs', 'package.json', 'package-lock.json']) {
    hashes[path] = sha(readFileSync(join(root, path)));
  }
  const provenance = Buffer.from(JSON.stringify({ tool: 'esbuild', version: '0.25.12', flags,
    sha256: hashes, probes: 'build-only asset reference, unresolved dynamic require, .node loader rejection' }, null, 2) + '\n');
  if (check) assert.deepEqual(readFileSync(join(root, 'provenance.json')), provenance);
  else writeFileSync(join(root, 'provenance.json'), provenance);
  console.log(check ? 'Bundles, maps, provenance and build-only probes match' : 'Generated bundles, maps and provenance; build-only probes passed');
} finally {
  rmSync(stage, { recursive: true, force: true });
}
