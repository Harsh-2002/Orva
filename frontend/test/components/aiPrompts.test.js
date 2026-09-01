import { describe, it, expect } from 'vitest'
import { buildBuildFailurePrompt, buildFixSuggestionPrompt } from '@/utils/aiPrompts'

// CONTRACT.md calls aiPrompts.js "documentation that executes": a false claim
// here is not a stale sentence, it is a prompt that sends the model after the
// wrong bug. A build failure has no request, no worker stderr and no status
// code, and the runtime prompt asserts all three.
describe('build-failure prompt', () => {
  const base = {
    source: 'import reqeusts\n',
    runtime: 'python',
    dependencies: 'reqeusts==2.0',
    buildLog: 'collecting\nERROR: No matching distribution found for reqeusts',
    errorMessage: 'pip install failed',
  }

  it('does not describe a build failure as a runtime failure', () => {
    const p = buildBuildFailurePrompt(base)
    expect(p).toMatch(/failed to BUILD/)
    expect(p).not.toMatch(/failed at runtime/)
    // The two things a build has none of, which the runtime prompt supplies.
    expect(p).not.toMatch(/<request>/)
    expect(p).not.toMatch(/triggered the failure outside the dashboard/)
  })

  it('carries the source, the dependencies, the log and the error', () => {
    const p = buildBuildFailurePrompt(base)
    for (const s of [base.source.trim(), base.dependencies, base.errorMessage,
      'No matching distribution found']) {
      expect(p, `prompt is missing: ${s}`).toContain(s)
    }
  })

  // npm and pip narrate for pages and say what broke on the last line, so the
  // head is the half that does not matter. The runtime prompt keeps the head.
  it('keeps the END of an oversized build log, not the start', () => {
    const log = `${'noise\n'.repeat(4000)}ERROR: the one line that matters`
    const p = buildBuildFailurePrompt({ ...base, buildLog: log })
    expect(p).toContain('ERROR: the one line that matters')
    expect(p).toMatch(/truncated — original was \d+ bytes/)
    expect(p.length, 'oversized log was not truncated at all').toBeLessThan(log.length)
  })

  it('still produces a usable prompt when nothing was captured', () => {
    const p = buildBuildFailurePrompt({ runtime: 'node' })
    expect(p).toMatch(/\(no build log captured\)/)
    expect(p).toMatch(/\(none declared\)/)
    expect(p).toMatch(/source unavailable/)
  })

  // Guards the split itself: the runtime prompt must keep saying the runtime
  // thing, or a future edit could quietly collapse the two back together.
  it('is a different prompt from the runtime one', () => {
    const runtime = buildFixSuggestionPrompt({ source: 'x', runtime: 'python' })
    expect(runtime).toMatch(/failed at runtime/)
    expect(runtime).not.toMatch(/failed to BUILD/)
  })
})
