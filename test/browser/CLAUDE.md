# test/browser/

Browser-driven UI verification: the checks that cannot be made by reading
source, because they are about geometry, computed style and real interaction.

```bash
cd test/browser
node run.mjs --url http://127.0.0.1:8443 --api-key "$ADMIN_KEY"
```

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
because they drive interactions rather than measure layout.

## Heuristics, and how they have been wrong

Every check here is a heuristic over computed style, and a heuristic that cries
wolf gets ignored. Three corrections have already been necessary — each one
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

If a check fires, read the source before changing the page. If the page is
right, fix the check and write down why here.

## Known limits

- Contrast is measured against the nearest opaque ancestor background; text over
  an image or gradient is not evaluated.
- CodeMirror's internals (generated class names like `ͼq`) surface in contrast
  and keyboard findings. The editor manages its own keyboard model, so those are
  reported but are not defects in this codebase's markup.
- `--destructive` flows mutate the instance. Point them at a scratch instance —
  `test/container/run.sh` brings one up for exactly this purpose.
