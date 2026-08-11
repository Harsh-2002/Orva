function normalize(value) {
  return String(value || '')
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
}

function subsequenceScore(text, query) {
  let score = 0
  let cursor = 0
  let previous = -2

  for (const character of query) {
    const match = text.indexOf(character, cursor)
    if (match === -1) return -1
    score += match === previous + 1 ? 4 : 1
    score -= Math.max(0, match - cursor)
    previous = match
    cursor = match + 1
  }

  return score
}

function termScore(words, compact, term) {
  let best = -1

  for (const word of words) {
    if (word === term) best = Math.max(best, 80)
    else if (word.startsWith(term)) best = Math.max(best, 60 - (word.length - term.length))
    else if (word.includes(term)) best = Math.max(best, 40 - word.indexOf(term))
  }

  const compactMatch = compact.indexOf(term)
  if (compactMatch >= 0) best = Math.max(best, 30 - compactMatch)
  return Math.max(best, subsequenceScore(compact, term))
}

// Model ids use inconsistent separators across providers (gpt-5-mini,
// claude_haiku, gemini.flash). Search normalizes those separators and scores
// each query term independently, so the way a human types the name does not
// have to mirror the provider's exact punctuation or word order.
export function modelSearchScore(model, query) {
  const fields = [model?.id, model?.label, model?.provider]
    .map(normalize)
    .filter(Boolean)
  const normalizedQuery = normalize(query)
  if (!normalizedQuery) return 0

  const words = fields.flatMap((field) => field.split(' '))
  const compact = fields.join(' ').replace(/\s+/g, '')
  const queryTerms = normalizedQuery.split(' ')
  const compactQuery = queryTerms.join('')

  let score = 0
  for (const term of queryTerms) {
    const match = termScore(words, compact, term)
    if (match < 0) return -1
    score += match
  }

  const compactAt = compact.indexOf(compactQuery)
  if (compactAt >= 0) score += 120 - compactAt
  if (fields.some((field) => field === normalizedQuery)) score += 200
  return score
}

export function filterModels(models, query) {
  const normalizedQuery = normalize(query)
  if (!normalizedQuery) return models

  return models
    .map((model, index) => ({ model, index, score: modelSearchScore(model, normalizedQuery) }))
    .filter((entry) => entry.score >= 0)
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .map((entry) => entry.model)
}
