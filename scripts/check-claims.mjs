#!/usr/bin/env node
/**
 * Fail the build when a load-bearing claim on this site no longer matches the
 * source it came from.
 *
 * Why this exists: this branch was last touched 364 commits before main, and in
 * that gap it drifted into publishing a handler signature that does not exist,
 * a licence the project has never used, a compose port map that would send a
 * reader to a dead socket, and a --cap-add that had been deliberately removed
 * upstream. None of that was one careless commit. All of it was code moving and
 * this page not.
 *
 * A check here is cheap. Add one whenever you publish a fact that lives in the
 * source rather than in your head.
 */

import { execFileSync } from 'node:child_process';
import { readFileSync, existsSync } from 'node:fs';

const RAW = 'https://raw.githubusercontent.com/Harsh-2002/Orva/main';

/**
 * Each claim names:
 *   what   a human-readable statement, printed on failure
 *   file   the path on main that settles it
 *   want   a regex that MUST match that file
 *   site   optional: a regex that must appear in the built site, so that
 *          changing the source also trips the check when the page still says
 *          the old thing
 */
const CLAIMS = [
  {
    what: 'Compose publishes host 3000 -> container 8443',
    file: 'docker-compose.yml',
    want: /"3000:8443"/,
    site: /host port.*3000|localhost:3000/i,
  },
  {
    what: "network_mode defaults to 'none'",
    file: 'backend/internal/database/migrations.go',
    want: /network_mode\s+TEXT NOT NULL DEFAULT 'none'/,
  },
  {
    what: 'seccomp is an enforcing allowlist with DEFAULT ERRNO(1)',
    file: 'backend/internal/sandbox/seccomp.go',
    want: /USE orva DEFAULT ERRNO\(1\)/,
  },
  {
    what: 'The Node handler is exports.handler = async (event)',
    file: 'docs/reference.md',
    want: /exports\.handler = async \(event\) =>/,
  },
  {
    what: 'The Python handler is def handler(event)',
    file: 'docs/reference.md',
    want: /def handler\(event\):/,
  },
  {
    what: 'event.body is documented as a raw string',
    file: 'docs/reference.md',
    want: /`body` is always the raw request body as a\s*\n?string/,
  },
  {
    what: 'Functions are invoked at /fn/<function_id>',
    file: 'docs/reference.md',
    want: /\/fn\/<function_id>/,
  },
  {
    what: 'The MCP server exposes 73 tools',
    file: 'docs/reference.md',
    want: /73 tools/,
    site: /73<\/span> tools|73 tools/,
  },
  {
    what: 'Cold start is ~50 to 500 ms, warm hit ~2 to 15 ms',
    file: 'docs/RUNTIMES.md',
    want: /~50[–-]500\s*ms[\s\S]{0,200}~2[–-]15\s*ms/,
  },
  {
    what: 'A warm worker costs ~18 MB idle',
    file: 'docs/DEPLOYMENT.md',
    want: /~18\s*MB when idle/,
  },
  {
    what: 'nsjail is hardcoded at /usr/local/bin/nsjail',
    file: 'backend/internal/config/defaults.go',
    want: /\/usr\/local\/bin\/nsjail/,
  },
  {
    what: 'The docker run recipe needs SYS_ADMIN, not NET_ADMIN',
    file: 'README.md',
    want: /--cap-add SYS_ADMIN/,
    forbidSite: /cap-add NET_ADMIN/,
  },
  {
    what: 'The project is Apache 2.0 licensed',
    file: 'LICENSE',
    want: /Apache License\s*\n\s*Version 2\.0/,
    forbidSite: /opensource\.org\/licenses\/MIT/,
  },
];

/** Read a path from main: local git first, then the network, then give up. */
async function fromMain(path) {
  try {
    return execFileSync('git', ['show', `main:${path}`], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
      maxBuffer: 32 * 1024 * 1024,
    });
  } catch {
    /* main is not in this clone: CI checks out the web branch shallow */
  }
  const res = await fetch(`${RAW}/${path}`);
  if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
  return res.text();
}

const siteHtml = ['dist/index.html', 'dist/docs/index.html']
  .filter(existsSync)
  .map((p) => readFileSync(p, 'utf8'))
  .join('\n');

let failed = 0;
let skipped = 0;

// Unreachable source is a WARNING locally and a FAILURE in CI.
//
// Skipping everywhere would be fail-open on a verification step, which is the
// exact shape of bug this file exists to catch. Failing everywhere would break
// an offline `npm run build` for a transient reason that has nothing to do with
// the change being built. CI has network and is the gate that matters.
const CI = !!process.env.CI;

for (const c of CLAIMS) {
  let src;
  try {
    src = await fromMain(c.file);
  } catch (err) {
    if (CI) {
      console.error(`  FAIL  ${c.what}`);
      console.error(`        could not read main:${c.file} (${err.message})`);
      console.error(`        In CI an unverifiable claim counts as a failed one.`);
      failed++;
    } else {
      console.warn(`  SKIP  ${c.what}`);
      console.warn(`        could not read main:${c.file} (${err.message})`);
      skipped++;
    }
    continue;
  }

  if (!c.want.test(src)) {
    console.error(`  FAIL  ${c.what}`);
    console.error(`        main:${c.file} no longer matches ${c.want}`);
    console.error(`        The source changed. Update the site, then this check.`);
    failed++;
    continue;
  }

  // The site checks only run once dist exists, so a first build is not blocked
  // by its own output not being there yet.
  if (siteHtml) {
    if (c.site && !c.site.test(siteHtml)) {
      console.error(`  FAIL  ${c.what}`);
      console.error(`        main says so, but the built site does not state it.`);
      failed++;
      continue;
    }
    if (c.forbidSite && c.forbidSite.test(siteHtml)) {
      console.error(`  FAIL  ${c.what}`);
      console.error(`        The built site still contains ${c.forbidSite}.`);
      failed++;
      continue;
    }
  }

  console.log(`  ok    ${c.what}`);
}

const total = CLAIMS.length;
console.log(
  `\n  ${total - failed - skipped}/${total} claims verified against main` +
    (skipped ? `, ${skipped} skipped (source unreachable, not CI)` : '') +
    (failed ? `, ${failed} FAILED` : '')
);

if (failed) {
  console.error('\n  A published claim no longer matches the source it came from.');
  process.exit(1);
}
