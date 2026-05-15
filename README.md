# Orva — landing page

Astro static site for [Orva](https://github.com/Harsh-2002/Orva). Lives on
the `web` branch, builds to `/dist`, deploys to GitHub Pages via
`.github/workflows/deploy.yml` on every push to this branch.

Live: <https://harsh-2002.github.io/Orva/>

## Run

```bash
npm install
npm run dev                              # http://localhost:4321/Orva
npm run build                            # outputs /dist
npx astro preview --port 4000 --host 0.0.0.0
```

Python's `http.server` cannot serve the built site directly because
`astro.config.mjs` sets `base: '/Orva'` and all asset URLs are prefixed
accordingly. Use `astro preview` (or any server that respects the base path).

## Layout

```
public/
  favicon.svg                 official f(x) brand mark
  screenshots/*.jpeg          dashboard captures shown in section 03
src/
  layouts/Layout.astro        HTML shell, lightbox, copy + scroll JS
  pages/index.astro           entire page composed inline (no components)
  styles/global.css           tokens, reset, shared primitives
astro.config.mjs              base: '/Orva', output: 'static'
```

## Notes

- The hero's "v…" pill is pulled from the GitHub Releases API at build
  time (`/repos/Harsh-2002/Orva/releases/latest`). Set `GITHUB_TOKEN` to
  avoid rate limits in CI. Falls back to a hardcoded tag if offline.
- Source code for Orva itself lives on `main`. This branch is
  landing-page only — no runtime, no SDK.

## License

MIT, same as [the Orva runtime](https://github.com/Harsh-2002/Orva/blob/main/LICENSE).
