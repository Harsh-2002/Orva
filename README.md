# Orva landing page

Astro static site for [Orva](https://github.com/Harsh-2002/Orva). Lives on the
`web` branch, builds to `/dist`, deploys to GitHub Pages via
`.github/workflows/deploy.yml` on every push to this branch.

Live: <https://harsh-2002.github.io/Orva/>

## Run

```bash
npm install
npm run dev                              # http://localhost:4321/Orva
npm run build                            # runs check-claims, then astro build
npm run lint                             # what CI gates on: types, eslint, format
npm run eslint                           # eslint on its own
npm run serve                            # built site on :3000, all interfaces
npx astro preview --port 4321 --host 0.0.0.0
```

Python's `http.server` cannot serve the built site directly, because
`astro.config.mjs` sets `base: '/Orva'` and every asset URL is prefixed
accordingly. Use `astro preview`, or any server that respects the base path.

## The one content rule

**The landing page sells. The docs explain.**

`index.astro` answers "what is this and why would I run it". It carries no
configuration caveats, no API reference, and no troubleshooting. A reader who
has not decided yet should never meet a gotcha; a reader who has decided is one
click from `/docs/`.

That boundary is easy to erode, because every caveat feels useful when you are
writing it. It had already eroded once: the landing page was explaining that
outbound network is off by default and that a call fails `ENETUNREACH` without
`network_mode: egress`, that nsjail must be installed or every invocation fails,
and that a key on the command line is visible via `ps`. All true, all important,
all reasons to hesitate rather than reasons to start.

Before adding a sentence to `index.astro`, ask which of these it is:

| Belongs on the landing page     | Belongs in the docs           |
| ------------------------------- | ----------------------------- |
| What it does, and for whom      | How to configure it           |
| Why it is trustworthy           | What breaks and why           |
| What you get with it            | API and event shapes          |
| What it deliberately is **not** | Flags, ports, paths, env vars |

The "what it is not" section is landing content on purpose. Naming the limits
of a security-adjacent tool builds more trust than hiding them. Naming its
_failure modes_ does not, and those are documentation.

## Layout

```
public/
  favicon.svg                 f(x) mark, #553F83
  og.png                      social card, generated from src/pages/og.astro
src/
  assets/*.png                dashboard captures, processed by astro:assets
  fonts/*.woff2               Inter + JetBrains Mono, variable, latin subset
  components/                 Brand, OrvaMark, Footer, ThemeDock, ThemeToggle,
                              CodeBlock, Shot
  layouts/Layout.astro        shell, theme plumbing, copy JS
  pages/index.astro           landing page
  pages/docs.astro            documentation, scroll-spy contents
  pages/404.astro             served by GitHub Pages for unknown paths
  pages/og.astro              social-card rendering surface, noindex
  styles/global.css           tokens (both themes), reset, primitives
  utils/highlight.ts          monochrome code tokenizer
scripts/check-claims.mjs      fails the build on a claim that drifted
astro.config.mjs              base: '/Orva', output: 'static'
```

**There is no header, and effectively no footer.** The brand is the first line
of the document. The theme control docks to the corner above `md` and moves into
the footer below it. The licence, the source and the invitation to contribute
are a real section at the end of the landing page rather than small print, so
all the footer still carries is a back-to-top link and, on narrow viewports,
the theme control. Nothing is sticky except the docs sidebar and the dock.

## The design system in four declarations

Everything in `global.css` reduces to these.

```css
html {
  font-size: 93.75%;
} /* one lever, 15px  */
@media (min-width: 64rem) {
  html {
    font-size: 100%;
  }
} /*            16px  */

*,
*::before,
*::after {
  letter-spacing: calc(0.72px - 0.04em);
}

:root {
  --pitch: 3.25rem;
  --v: 1.4rem;
}
```

The second line is the entire optical tracking curve, at every size,
continuously. It has to be on `*` rather than `:root`: an inherited
`letter-spacing` computes to an absolute length at the declaring element and is
passed down as that length, so on `:root` every element would inherit the root's
tracking instead of deriving its own. For the same reason the form-control reset
must not set `letter-spacing: inherit` — a type selector beats the universal one
and every button would silently opt out of the curve.

Light and dark are re-authored twins: every token is chosen independently and
matched to its counterpart in OKLCH lightness delta, never by inverting a hex.
Both ladders are warm (hue 88 light, 80 dark) so the toggle does not flip the
hue of the largest surface on screen. Chroma is tiny by design; it should read
as paper and ink, never as beige.

Worst-case text contrast is **5.05 light / 4.95 dark**, which is `--ink-3` on
`--s5`. `--ink-4` is a non-text token: rules and decorative marks only.

## Fonts and layout shift

`font-display: swap` paints a fallback first and re-paints when the real face
arrives. If the two occupy different space, everything below the swapped text
jumps. Measured at 412px, the hero CTA sat **44px lower** once Inter loaded, and
that is above the fold, so Lighthouse scored CLS at 0.283.

`global.css` declares an `Inter Fallback` face whose `size-adjust`,
`ascent-override` and `descent-override` make the fallback occupy Inter's box
exactly. All four numbers are measured in the browser, not copied: the ratio of
a body-copy string in each face at weight 400, and Inter's own ascent and
descent divided by that ratio. CLS is now 0.

JetBrains Mono needs no equivalent; against the system monospace it measured
99.64%, which cannot move a line.

Note that this does not reproduce locally once the fonts are in the HTTP cache,
which is why the first three attempts to observe it found nothing. Lighthouse
clears cache on every run.

## Browser chrome

Three things make the browser's own surfaces match the page, and all three move
together when `--page` changes:

- Two media-scoped `theme-color` metas for the system case, which the theme
  toggle swaps for a single unscoped tag the moment the reader picks a specific
  theme. A scoped light tag loses to a system-dark phone.
- `background` on `html`, not just `body`. That is the rubber-band overscroll
  strip on iOS and macOS, which sits directly against the toolbar.
- `scrollbar-color`, so the gutter is not a neutral grey beside a warm page.

No `*-web-app-capable` metas, deliberately: they declare standalone launch, and
a docs site with outbound links in standalone mode is a trap with no back
button.

## Screenshots

`src/assets/*.png` are real Safari captures of a live instance. They carry their
own window chrome, so `Shot.astro` adds nothing but a corner radius and one
shadow. `astro:assets` emits WebP at three widths; the source PNGs never ship.

Two things to preserve when replacing them:

- Capture against **current `main`**, whose canvas is `#0B0D10`. An earlier set
  came from a build with a violet `#13111C` canvas, which contradicted the
  neutral-graphite rule the page exists to demonstrate.
- Keep the aspect ratio consistent. `.shot` declares `3164 / 2070` so the box
  reserves its exact height before the image decodes. The previous site declared
  1.6 against an intrinsic 1.529 and silently cropped 4.4% off every capture.

## The social card

`public/og.png` is a screenshot of `src/pages/og.astro`, so it is built from the
real stylesheet and the real fonts and cannot drift from the design by hand.

To regenerate: serve the site, open `/Orva/og/`, force `data-theme="dark"`,
screenshot at exactly 1200x630, save over `public/og.png`.

The previous card was left over from the old design, on a `#0e0d17` violet that
exists nowhere here any more. A stale card is invisible locally and shows up on
every share.

## Linting

`npm run lint` is what CI gates on, and it is three checks: `astro check` for
types, `eslint .` for correctness, and `prettier --check` for formatting.

The ESLint config is deliberately small, and two rules are there because their
absence cost something real:

- **`no-empty` with `allowEmptyCatch: false`.** The copy button called
  `navigator.clipboard.writeText` unconditionally and swallowed the resulting
  `TypeError` in an empty catch. On any non-secure origin, which is how this
  site is reviewed and how many people serve Orva, the button did nothing and
  said nothing about it.
- **`no-unused-vars`.** Unused code is the residue of a half-finished change.

Note that ESLint flat config is **last-wins**: an override block has to come
after the general rules block or it is silently discarded. The `scripts/`
override sat above the general rules at first, which quietly re-enabled
`no-console` for a set of files whose entire job is console output.

Accessibility rules come from `eslint-plugin-astro`'s own
`flat/jsx-a11y-recommended`, which is 36 rules over `.astro` templates.
`eslint-plugin-jsx-a11y`'s peer range still stops at ESLint 9 while
`eslint-plugin-astro@3` requires >=10, so `package.json` pins its `eslint` peer
to the root version through `overrides`. That is a stale range on their side,
not a real incompatibility: the rules are verified to fire.

## Claims

`npm run build` runs `scripts/check-claims.mjs` first. It reads the real source
on `main` (local git, falling back to raw.githubusercontent.com) and fails the
build if a load-bearing claim on this site no longer matches.

An unreachable source is a warning locally and a **failure in CI**: skipping
everywhere would be fail-open on a verification step, which is the exact shape
of bug the file exists to catch.

This exists because it was missing. This branch sat 364 commits behind `main`
and drifted into publishing a handler signature that does not exist, a licence
the project has never used, a compose port that would send a reader to a dead
socket, and a `--cap-add` that had been deliberately removed upstream. Add a
check whenever you publish a fact that lives in the source.

## Notes

- Source for Orva itself is on `main`. This branch is landing page only: no
  runtime, no SDK.
- No version number is hardcoded anywhere. Orva prunes old releases and their
  tags, so a pinned `vYYYY.MM.DD` 404s the next time one ships. Link to
  `releases/latest`.
- `main`'s own `favicon.svg` uses `#7C3AED`, which is not the brand violet the
  dashboard renders. This site uses `#553F83`, verified against the pixels in
  the screenshots.

## License

Apache 2.0, same as
[the Orva runtime](https://github.com/Harsh-2002/Orva/blob/main/LICENSE).
