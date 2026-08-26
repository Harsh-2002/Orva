/**
 * Monochrome tokenizer. Three classes, no hue.
 *
 *   tok-k  keywords and commands   --ink, weight 600
 *   tok-s  strings and numbers     --ink-2
 *   tok-c  comments and the prompt --ink-3
 *
 * A code block is the largest source of unmanaged colour on a developer landing
 * page, and a rainbow-highlighted line forks the palette onto a surface the
 * design system does not govern. Scanning here is carried by weight and value
 * instead, which is what the Graphite Neutral Rule already says about
 * everything else on the page.
 *
 * This is deliberately a small tokenizer rather than a grammar. It is used on
 * fewer than a dozen short, hand-authored snippets; a full highlighter would be
 * a dependency, a theme to maintain, and a second place for colour to enter.
 */

const KEYWORDS: Record<string, string[]> = {
  js: [
    'const',
    'let',
    'var',
    'function',
    'return',
    'await',
    'async',
    'exports',
    'require',
    'import',
    'from',
    'export',
    'default',
    'new',
    'if',
    'else',
    'try',
    'catch',
    'throw',
    'typeof',
  ],
  ts: [
    'const',
    'let',
    'var',
    'function',
    'return',
    'await',
    'async',
    'import',
    'from',
    'export',
    'default',
    'interface',
    'type',
    'new',
    'if',
    'else',
    'try',
    'catch',
    'throw',
    'as',
    'satisfies',
  ],
  python: [
    'def',
    'return',
    'import',
    'from',
    'as',
    'if',
    'elif',
    'else',
    'try',
    'except',
    'raise',
    'with',
    'for',
    'in',
    'while',
    'class',
    'lambda',
    'None',
    'True',
    'False',
    'not',
    'and',
    'or',
    'pass',
  ],
  yaml: [],
  bash: [],
  http: [],
};

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

/** Wrap the run of text in a token class, escaping it on the way. */
const tok = (cls: string, s: string) => `<span class="${cls}">${esc(s)}</span>`;

function highlightLine(line: string, lang: string): string {
  // Comments win outright: everything from the marker to end of line is a
  // comment, including anything that would otherwise look like a string.
  const commentMarker = lang === 'js' || lang === 'ts' ? '//' : '#';
  const ci = findUnquoted(line, commentMarker);
  if (ci >= 0) {
    return highlightLine(line.slice(0, ci), lang) + tok('tok-c', line.slice(ci));
  }

  // A shell prompt is chrome, not code. It reads as the faintest thing on the
  // line so the command itself is what the eye lands on.
  if (lang === 'bash') {
    const m = line.match(/^(\s*)(\$ )(.*)$/);
    if (m) {
      const [, ws, prompt, rest] = m;
      const cmd = rest.match(/^([\w./-]+)([\s\S]*)$/);
      const body = cmd ? tok('tok-k', cmd[1]) + highlightStrings(cmd[2]) : highlightStrings(rest);
      return esc(ws) + tok('tok-c', prompt) + body;
    }
    return highlightStrings(line);
  }

  let out = highlightStrings(line);

  const kws = KEYWORDS[lang];
  if (kws?.length) {
    // Only match outside an already-emitted span, so a keyword inside a string
    // literal is not re-tagged.
    out = out.replace(new RegExp(`(?<!<span[^>]*>)\\b(${kws.join('|')})\\b(?![^<]*<\\/span>)`, 'g'), (m) =>
      tok('tok-k', m)
    );
  }

  return out;
}

/** Index of `needle` outside any quoted run, or -1. */
function findUnquoted(line: string, needle: string): number {
  let q: string | null = null;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (q) {
      if (c === '\\') i++;
      else if (c === q) q = null;
    } else if (c === '"' || c === "'" || c === '`') {
      q = c;
    } else if (line.startsWith(needle, i)) {
      return i;
    }
  }
  return -1;
}

function highlightStrings(s: string): string {
  let out = '';
  let buf = '';
  let q: string | null = null;
  let sbuf = '';

  const flush = () => {
    if (buf) {
      // Numbers read as literals, the same rung as strings.
      out += esc(buf).replace(/\b(\d[\d_.]*)\b/g, (m) => tok('tok-s', m));
      buf = '';
    }
  };

  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (q) {
      sbuf += c;
      if (c === '\\' && i + 1 < s.length) {
        sbuf += s[++i];
      } else if (c === q) {
        out += tok('tok-s', sbuf);
        sbuf = '';
        q = null;
      }
    } else if (c === '"' || c === "'" || c === '`') {
      flush();
      q = c;
      sbuf = c;
    } else {
      buf += c;
    }
  }

  if (q) out += tok('tok-s', sbuf); // unterminated: still a literal
  flush();
  return out;
}

export function highlight(code: string, lang = 'bash'): string {
  return code
    .replace(/\n+$/, '')
    .split('\n')
    .map((l) => highlightLine(l, lang))
    .join('\n');
}
