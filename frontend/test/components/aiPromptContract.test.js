import { describe, it, expect } from 'vitest'
import { buildPromptText } from '@/utils/aiPrompts'

// CONTRACT.md calls aiPrompts.js "documentation that executes": a false claim
// here is not a stale sentence, it is generated code that does not run.
//
// The prompt used to say the orva module was "pre-imported". It is not.
// Measured on a real nsjail sandbox, both runtimes, with the import omitted:
//   python  ->  500  NameError: name 'orva' is not defined
//   node    ->  500  ReferenceError: orva is not defined
// With the import, both return 200. Neither adapter injects a global; the
// module is on the path, which is a different promise.
describe('the SDK import claim in the AI system prompt', () => {
  const prompt = buildPromptText({ runtime: 'python', description: 'x' })

  it('does not tell the model the module is already imported', () => {
    expect(prompt).not.toMatch(/pre-imported/i)
    expect(prompt).not.toMatch(/already imported/i)
  })

  it('names the import for both runtimes', () => {
    expect(prompt).toContain('import orva')
    expect(prompt).toContain("require('orva')")
  })

  it('says what happens when the import is missing, so the model does not skip it', () => {
    expect(prompt).toMatch(/NameError/)
    expect(prompt).toMatch(/ReferenceError/)
  })

  // The other half of the same audit: where a handler's output actually goes.
  // stdout is the framed protocol channel, and /activity is the API audit
  // trail, so the old "Logs land on the Activity page and on stdout" was
  // wrong twice in one sentence.
  it('does not promise stdout or the Activity page for handler output', () => {
    expect(prompt).not.toContain('Activity page and on stdout')
  })
})
