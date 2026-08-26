# test/browser/

Browser-driven UI verification: the checks that cannot be made by reading
source, because they are about geometry, computed style and real interaction.

```bash
cd test/browser
node run.mjs --url http://127.0.0.1:8443 --api-key "$ADMIN_KEY"
node run.mjs --url ... --theme day        # one theme instead of both
```

**Both themes run by default.** The dashboard ships a night and a day palette
over identical markup, so a single-theme pass proves nothing about the other
one: the runner sets `localStorage['orva:theme']` in an init script (the page
resolves the theme before first paint, so setting it after load is too late)
and matches `colorScheme` on the context, because a headless Chromium reports
`light` and would otherwise make every "night" run a lie. The theme is prefixed
onto every result's location, so a failure reads `day laptop /deployments`.

It obtains a dashboard session by logging in as `browser-harness`, onboarding
that user if it does not exist (which is why `--api-key` is needed on an
instance that already has functions). Add `--destructive` to run flows that
delete data; without it those are skipped, not silently passed.

## Why a browser is required

`html, body { overflow-x: hidden }` is deliberate (DESIGN.md §7): the document
never scrolls sideways. The cost is that a container which exceeds its width is
**clipped silently** — no scrollbar, no visual cue, nothing in the markup that
says "this got cut off". The box has to be measured. Two real defects of exactly
this shape shipped before this suite existed.

`html { font-size: 95% }` is the other reason. 1rem is 15.2px, so `h-11` is
41.8px — under the 44px touch floor — and `text-base` is 15.2px, which does not
clear iOS's 16px focus-zoom threshold. Neither is visible in the class name.

## Suites

| Suite | Asks |
|---|---|
| `smoke` | every route renders, no console errors, no failed requests |
| `responsive` | nothing overflows the viewport; nothing is clipped without a scroll affordance |
| `touch-targets` | every control clears 44×44 where the pointer is coarse |
| `accessibility` | accessible names, keyboard reachability, AA contrast, heading order, duplicate ids |
| `journeys` | real multi-step flows (nav drawer, destructive-dialog keyboard safety) |

Layout suites run per viewport. Flow suites run once, on the widest viewport,
because they drive interactions rather than measure layout. The accessibility
suite runs on `phone` and `laptop`: below `sm` the list views render an entirely
different subtree (cards, not a table) and the sidebar becomes a drawer, so a
laptop-only pass never measured any of it.

## Heuristics, and how they have been wrong

Every check here is a heuristic over computed style, and a heuristic that cries
wolf gets ignored. Five corrections have already been necessary — each one
found by chasing a reported failure into the source and discovering the page was
right:

- **`cursor: pointer` inherits.** A clickable `<tr>` makes every one of its
  `<td>`s match the "click handler on a non-interactive element" test. The check
  now attributes the finding to the outermost element sharing the style — the
  one that actually owns the handler — and skips it entirely if that element
  contains a focusable control. The Activity table deliberately pairs a
  row-level `@click` with a real `<button>` in its first cell; it was being
  flagged six times for a defect it had already solved.
- **`innerText` is layout-aware.** It returns `""` for anything inside a
  collapsed section, so a `<label for=…>` that reads "Provider" and a button
  reading "Save provider" were both reported as having no accessible name.
  Names are computed from `textContent`, which is what accname specifies.
- **`placeholder` is a real name source** for text inputs per HTML-AAM, ranked
  below `label` and above `title`. It is weak labelling — it disappears the
  moment the field has a value — but "weak name" is a different finding from
  "no name", and conflating them buries the fields that truly have nothing.
- **A `<label>` names its control by its text *alternative*, not its text
  content.** An `aria-label` on the label counts, and outranks the text inside
  it. Reading only `textContent` reported all 33 row checkboxes in the
  `/invocations` card view as unnamed; the wrapping label carries
  `aria-label="Select invocation <id>"` and Chrome's own AX tree resolves
  exactly that string. Checked against `Accessibility.getFullAXTree` over CDP
  before the probe was changed, not after.
- **Computed colour is not always `rgb()`.** Tailwind v4 compiles every `/NN`
  alpha to `color-mix(in oklab, …)`, which Chrome serialises as `oklab()`. A
  parser that understood only `rgb()` returned null and the caller skipped the
  element, so the contrast check silently exempted precisely the faded text it
  exists to catch. It now converts oklab/oklch to sRGB, composites translucent
  *text* as well as translucent backgrounds, and takes its last-resort canvas
  colour from the document instead of a hardcoded near-black that belonged to
  one theme.

If a check fires, read the source before changing the page. If the page is
right, fix the check and write down why here.

## Known limits

- Contrast is measured against the nearest opaque ancestor background; text over
  an image or gradient is not evaluated.
- **Placeholder text is not covered.** It has no text node, so the 1.4.3 walk
  never reaches it. It is a real gap: measured by hand, placeholders at `/50`
  and `/60` were failing AA in both themes. `frontend/test/responsive.test.js`
  now bans the alpha at source instead, which is cheaper than teaching this
  suite to read `::placeholder`.
- **Non-text contrast (1.4.11) is not covered** beyond what a text ratio
  implies: control borders, icon-only buttons and focus rings are not measured
  here. Several of those rules need a human to separate a decorative divider
  from a component boundary, and a heuristic that cries wolf gets ignored.
- CodeMirror's internals (generated class names like `ͼq`) surface in contrast
  and keyboard findings. The editor manages its own keyboard model, so those are
  reported but are not defects in this codebase's markup.
- `--destructive` flows mutate the instance. Point them at a scratch instance —
  `test/container/run.sh` brings one up for exactly this purpose.
