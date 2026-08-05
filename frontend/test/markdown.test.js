import test from 'node:test'
import assert from 'node:assert/strict'
import { createMarkdownRenderer } from '../src/utils/markdown.js'

test('assistant markdown linkifies bare domains', () => {
  const markdown = createMarkdownRenderer({ linkify: true })

  assert.equal(
    markdown.renderInline('example.com'),
    '<a href="http://example.com">example.com</a>',
  )
})
