import MarkdownIt from 'markdown-it'

// markdown-it 15 upgrades linkify-it to v6, which disables fuzzy bare-domain
// links by default. Orva historically linkified these in assistant messages,
// so keep that user-facing behavior explicit instead of relying on defaults.
export function createMarkdownRenderer(options = {}) {
  const markdown = new MarkdownIt(options)
  markdown.linkify.set({ fuzzyLink: true })
  return markdown
}
