import test from 'node:test'
import assert from 'node:assert/strict'
import { filterModels, modelSearchScore } from '../src/utils/modelSearch.js'

const models = [
  { id: 'gpt-5', label: 'GPT 5', provider: 'openai' },
  { id: 'gpt-5-mini', label: 'GPT 5 Mini', provider: 'openai' },
  { id: 'claude-sonnet-4-5', label: 'Claude Sonnet 4.5', provider: 'anthropic' },
  { id: 'gemini-2.5-flash', label: 'Gemini 2.5 Flash', provider: 'gemini' },
]

test('model search ignores provider separators', () => {
  assert.equal(filterModels(models, 'gpt 5 mini')[0].id, 'gpt-5-mini')
  assert.equal(filterModels(models, 'sonnet 4.5')[0].id, 'claude-sonnet-4-5')
})

test('model search matches terms in the order a human remembers them', () => {
  assert.equal(filterModels(models, 'mini gpt')[0].id, 'gpt-5-mini')
  assert.equal(filterModels(models, 'flash gemini')[0].id, 'gemini-2.5-flash')
})

test('model search supports compact and fuzzy queries', () => {
  assert.equal(filterModels(models, 'gpt5mini')[0].id, 'gpt-5-mini')
  assert.ok(modelSearchScore(models[2], 'cldsnnt') >= 0)
})

test('model search excludes unrelated models and preserves the list when empty', () => {
  assert.deepEqual(filterModels(models, 'llama'), [])
  assert.equal(filterModels(models, ''), models)
})
