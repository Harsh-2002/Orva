---
name: Orva
description: Self-hosted Function-as-a-Service for homelab operators and on-prem teams.
colors:
  background: "#0B0D10"
  surface: "#15181D"
  surface-hover: "#23272E"
  border: "#343A44"
  foreground: "#F4F6F8"
  foreground-muted: "#ADB4BE"
  primary: "#553F83"
  primary-hover: "#684D9E"
  primary-foreground: "#FFFFFF"
  secondary: "#282E36"
  secondary-hover: "#353D47"
  danger: "#EF4444"
  warning: "#EAB308"
  success: "#22C55E"
  runtime-node: "#8FBF7F"
  runtime-python: "#7FA8D4"
typography:
  display:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(1.875rem, 4vw, 2.25rem)"
    fontWeight: 600
    lineHeight: 1.1
    letterSpacing: "-0.02em"
  headline:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.25rem"
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: "-0.015em"
  title:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-0.01em"
  body:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.55
  label:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 700
    lineHeight: 1.2
    letterSpacing: "0.08em"
  mono:
    fontFamily: "JetBrains Mono, ui-monospace, SFMono-Regular, monospace"
    fontSize: "0.875rem"
    fontWeight: 500
    lineHeight: 1.5
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  pill: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "16px"
  lg: "20px"
  xl: "24px"
  "2xl": "32px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.md}"
    padding: "0 16px"
    height: "40px"
  button-primary-hover:
    backgroundColor: "{colors.primary-hover}"
  button-secondary:
    backgroundColor: "{colors.secondary}"
    textColor: "{colors.foreground}"
    rounded: "{rounded.md}"
    padding: "0 16px"
    height: "40px"
  button-danger:
    backgroundColor: "{colors.danger}"
    textColor: "{colors.foreground}"
    rounded: "{rounded.md}"
    padding: "0 16px"
    height: "40px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.foreground-muted}"
    rounded: "{rounded.md}"
    padding: "0 16px"
    height: "40px"
  chip:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.foreground-muted}"
    rounded: "{rounded.md}"
    padding: "0 10px"
    height: "28px"
  chip-active:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
  card:
    backgroundColor: "{colors.background}"
    rounded: "{rounded.lg}"
    padding: "20px"
  card-elevated:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.lg}"
    padding: "20px"
  input:
    backgroundColor: "{colors.background}"
    textColor: "{colors.foreground}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
    height: "36px"
  modal:
    backgroundColor: "{colors.background}"
    rounded: "{rounded.lg}"
    padding: "20px"
  status-pill:
    backgroundColor: "{colors.background}"
    textColor: "{colors.foreground-muted}"
    rounded: "{rounded.sm}"
    padding: "2px 8px"
    height: "20px"
---

# Design System: Orva

## 1. Overview

**Creative North Star: "The Late-Night Lab Notebook"**

Orva is the control plane for someone running a function platform on their own machine. Probably in a dim home office, often after-hours, more curious than time-pressured. The interface earns trust by behaving like a serious infrastructure tool: dense, unfussy, comfortable with code, willing to show numbers without dressing them up. It doesn't perform competence; it embodies it. Every page subhead reads as a one-line operator's note from a colleague who already knows the system: short, factual, no marketing flair.

The palette is the neutral graphite-black of an editor at midnight, paired with a single muted violet accent that carries every CTA. JetBrains Mono runs alongside Inter so paths, IDs, byte counts, and CPU readings sit in their natural register without arguing for attention. Rhythm is built from quiet density: small type, generous gaps where ideas separate, tight gaps within a single thought, no empty-space-as-decoration. Surfaces are flat. Shadows whisper, never advertise.

What this system rejects: the SaaS-marketing dashboard family (gradient hero panels, three-up icon-feature cards, glassy decorative blurs), category clichés (cloud-vendor purple gradients, "modern" minimal-cream-with-emerald, observability navy), and copy that sounds written rather than spoken. Orva is operator software for people who chose to self-host, not a sales pitch for becoming a self-hoster.

**Key Characteristics:**
- Neutral graphite-black surfaces; one muted violet accent carries identity.
- Inter for prose, JetBrains Mono for any data the operator might compare digit-to-digit.
- Standardised page heads: `text-xl semibold tracking-tight` over a one-line subhead in muted text, max-w-prose.
- Thin, low-contrast scrollbars keep nested overflow discoverable without dominating the surface.
- Status colour is semantic, never decorative. A pill that's amber means in-flight; an amber accent that means nothing is forbidden.
- No copy uses em dashes. Periods, commas, colons, semicolons, parentheses. The voice is operator-spoken.

## 2. Colors

A single muted violet accent sits on neutral graphite-black surfaces with cool light-gray text and three semantic status colours. Neutral surfaces maximize reading contrast; brand color is reserved for actions and selection.

### Primary
- **Muted Violet** (`#553F83`, ≈`oklch(40% 0.10 295)`): the only identity colour. Carries primary CTAs (Deploy, Save, Confirm), the active sidebar item, status-running indicators, and selected chip filters. Reserved enough that when it appears, the eye knows where to land.
- **Muted Violet Hover** (`#684D9E`, ≈`oklch(48% 0.12 295)`): hover state for primary surfaces. Lightness step only; same hue and chroma family.

### Neutral
- **Background** (`#0B0D10`): the near-black application canvas and card body. It is neutral graphite, not violet.
- **Surface** (`#15181D`): one graphite elevation step up. Used for headers, footers, modal layers, and inline code blocks.
- **Surface Hover** (`#23272E`): hover state for rows, list items, and ghost buttons. It stays neutral so interaction does not compete with semantic color.
- **Border** (`#343A44`): dividers, card outlines, and input strokes. Slightly cool gray keeps dense structure visible without drawing a grid over the page.
- **Foreground** (`#F4F6F8`): soft light gray for primary reading text. `--color-foreground-strong: #FFFFFF` remains reserved for saturated brand surfaces.
- **Foreground Muted** (`#ADB4BE`): secondary text, icon defaults, and lower-ranked table content. It maintains at least 8.6:1 contrast against both primary surfaces.
- **Link** (`--color-link: #8b7bd8`): hyperlinks inside rendered prose (the AI chat markdown, Docs). A lighter, less-saturated lift of the violet accent so links read as interactive without competing with primary CTAs. Inline code in the same prose uses `--color-surface-hover` as its chip background.

### Status (semantic, used at low chroma)
- **Success** (`#22C55E`, ≈`oklch(73% 0.21 144)`): only for terminal-success states. Pair with the recommended `success/15` tint background and `success/30` border (see Named Rule below).
- **Warning** (`#EAB308`, ≈`oklch(78% 0.16 86)`): in-flight, queued, soft warnings ("expires in 7 days"), drift hints.
- **Danger** (`#EF4444`, ≈`oklch(64% 0.23 25)`): destructive actions, failed states, error toasts.

### Named Rules

**The One Accent Rule.** Muted violet appears on roughly 5 to 10 percent of any given screen. Primary CTAs, the active sidebar item, the selected filter chip. Anywhere else, ask whether the page actually needs an accent or whether a border + foreground-muted will read more confidently. Restraint is the point.

**The Graphite Neutral Rule.** Canvas and elevated surfaces stay neutral graphite. Violet communicates action, selection, and focus only. Primary text is soft gray; pure white is reserved for saturated action surfaces.

**The Semantic Status Rule.** Status colour is reserved for status. Use the semantic tokens (`success`, `warning`, `danger`) and their `/15` tint backgrounds and `/30` borders. Reaching for `bg-emerald-500/40` or `text-sky-300` is forbidden, even when it looks "right" in isolation: that path forks the palette across views and a future theme change becomes a 125-site rewrite.

## 3. Typography

**Display + Body Font:** Inter (300 / 400 / 500 / 600 weights), with `ui-sans-serif, system-ui, sans-serif` as the fallback stack.
**Mono Font:** JetBrains Mono (400 / 500), with `ui-monospace, SFMono-Regular, monospace` as the fallback stack.
Both load from Google Fonts via `<link rel="preconnect">` plus a single combined stylesheet.

**Character:** Inter is the dashboard's spoken voice; JetBrains Mono is its hand. Anything an operator might compare digit-by-digit (a UUID, a memory reading, a port, an HTTP method) sits in the mono register so the eye can scan vertically without re-anchoring. Uppercase tracked labels mark section captions; everything else stays in mixed case.

### Hierarchy

- **Display** (Inter, weight 600, `clamp(1.875rem, 4vw, 2.25rem)`, line-height 1.1, tracking -0.02em): the 404 wordmark, the Onboarding hero. Rare. Reserved for moments that frame the whole product.
- **Headline** (Inter, weight 600, `1.25rem` / 20px, line-height 1.2, tracking -0.015em): every page H1. The standardised dashboard header, no exceptions.
- **Title** (Inter, weight 600, `0.875rem` / 14px, tracking -0.01em): modal headers, drawer headers, panel titles inside the editor.
- **Body** (Inter, weight 400, `0.875rem` / 14px, line-height 1.55): the dashboard's default. Page subheads, table cells, paragraph copy. Cap reading-flow paragraphs at `max-w-prose` (~65–75ch).
- **Label** (Inter, weight 700, `0.75rem` / 12px, tracking 0.08em, uppercase): section captions inside cards ("Response time", "Host machine", "Builds"). Currently rendered as styled `<div>`s; should be promoted to `<h2>`/`<h3>` for screen-reader semantics.
- **Mono Body** (JetBrains Mono, weight 500, `0.875rem` / 14px): paths, IDs, request bodies, code editors, raw JSON.
- **Mono Tile** (JetBrains Mono, weight 400, `1.125rem` / 18px): the at-a-glance stat values on the Overview page (CPU cores, MB reserved, p95). Slightly larger so the operator's first glance lands here.
- **Micro** (Inter, weight 400, `0.6875rem` / 11px or `0.625rem` / 10px, tracking 0.04em uppercase for labels): hint copy under metrics, drawer micro-labels. Use sparingly; if the operator needs to read it, it should be Body, not Micro.

### Named Rules

**The Operator's Mono Rule.** If two characters at the same column should be visually compared (a UUID against another UUID, a memory reading against the limit), they are mono. Mixing them with Inter forces the eye to re-anchor.

**The No-Em-Dash Rule.** Em dashes are forbidden in any user-facing string, including subheads, alerts, empty-states, table placeholders, and toasts. Use periods, commas, colons, semicolons, or parentheses. The voice this dashboard is reaching for is spoken, not written; the moment a sentence needs an em dash, rewrite it.

**The Heading Hierarchy Rule.** Every page has exactly one `<h1>` (the standardised page header). Section captions inside the page must be real `<h2>` (or `<h3>` when nested), even when styled as small uppercase tracked labels. Screen readers navigate by heading; styled `<div>`s are invisible to them.

## 4. Elevation

Orva is flat by default. Layers separate through borders and faint background steps (background → surface → surface-hover), not through shadows. Where shadow does appear, it is functional: a soft purple glow under the primary CTA, a deeper drop on modals, a low ambient shadow under the active sidebar item to anchor focus.

Glassmorphism is rare and earned. The only legitimate use is `backdrop-blur-sm` on modal/drawer backdrops where the blur signals that the page underneath is no longer interactive. Decorative blurs on icon chips or feature panels are forbidden.

### Shadow Vocabulary

- **CTA Glow** (`box-shadow: 0 4px 6px -1px rgba(85,63,131,0.20), 0 2px 4px -2px rgba(85,63,131,0.20)`): primary buttons. Says "this is the action". Subtle, hue-matched.
- **Danger Glow** (`box-shadow: 0 4px 6px -1px rgba(239,68,68,0.30), 0 2px 4px -2px rgba(239,68,68,0.30)`): destructive buttons. Slightly stronger so the operator pauses.
- **Sidebar Active** (`box-shadow: 0 10px 15px -3px rgba(38,18,87,0.20)`): the currently-routed nav item. Anchors the user's "you are here".
- **Modal Drop** (`box-shadow: 0 25px 50px -12px rgba(0,0,0,0.50)`): modal containers. Visually separates a dialog from the dimmed page beneath.

### Named Rules

**The Flat-By-Default Rule.** Cards, list items, drawers, and inline panels carry no shadow at rest. Depth is read from borders and the background → surface → surface-hover ladder. Shadow appears only as functional response: action affordance, danger affordance, modal lift, active-route anchor.

**The Earned Blur Rule.** `backdrop-blur` exists in this codebase only on the dimmed background that sits behind a modal or drawer. It is not decoration. Glassmorphic icon chips, glassy feature tiles, blurred decorative circles: forbidden.

## 5. Components

### Buttons (`components/common/Button.vue`)

The dashboard's interactive primitive. Five variants, four sizes.

- **Shape:** `rounded-md` (6px). Tight enough to feel technical; not pill-shaped.
- **Primary:** muted-violet fill, white foreground, CTA glow shadow. Used for the page-level affirmative action (Deploy, Save, Confirm).
- **Secondary:** surface fill, bordered, white foreground. Page-level companion (Refresh, Cancel-as-non-destructive).
- **Danger:** danger fill, white foreground, danger glow. Confirm-destruction.
- **Ghost:** transparent, foreground-muted, fills surface-hover on hover. Tertiary actions, modal Cancel.
- **Chip:** unfilled border at rest; flips to primary fill when active. Filter pill toggles on Jobs/Webhooks/CronJobs status strips.
- **States:** `:hover` lightens fill by one step; `:focus-visible` shows a 2px primary ring offset by `--color-background`; `:disabled` 50% opacity + not-allowed cursor.

**Sizes:** xs `h-7 px-2.5`, sm `h-8 px-3`, md `h-10 px-4` (default), lg `h-12 px-6`. Rendered heights are 26.6 / 30.4 / 38 / 45.6px, not the nominal 28 / 32 / 40 / 48, because the root is scaled to 95% (section 7). Only `lg` clears the 44px touch floor on its own, so xs, sm **and md** carry a `touch-expand-*` that grows the real box on coarse pointers. md was long assumed compliant at a nominal 40px and was not.

### Filter Chips

Filter pills on `Jobs.vue`, `Webhooks.vue`, `CronJobs.vue` status strips. Same component as Button, `variant="chip"`.

- **At rest:** `bg-surface text-foreground-muted border border-border`.
- **Hover:** `text-white border-foreground-muted`.
- **Active:** `bg-primary text-primary-foreground border-primary`. No shadow — chips sit flat on the surface, unlike CTAs which lift.

### Cards

Two parallel conventions exist in the codebase; document both honestly.

- **Page-Level Card** (used in `Dashboard.vue` for tiles, `Activity.vue` for stat strips, etc.): `bg-background border border-border rounded-lg p-5`. The page itself is `bg-surface`-shaped (via the layout shell), so cards are a step *down* into the deep background. Use a semantic `h2` or `h3` at `text-sm font-semibold`; add hint copy only when the metric cannot explain itself.
- **Component Card** (`components/common/Card.vue`): `bg-surface border border-border rounded-lg`, optional header/footer slots with `border-b/t border-border` dividers and a `bg-surface/50` footer tint. Hoverable variant adds `hover:border-foreground-muted`.

**Padding scale:** `none / sm: p-4 / normal: px-6 py-4 / lg: p-8`. Dashboard tiles use `p-5` (20px) inline, which is the rhythm step between sm and normal — keep.

### Inputs (`components/common/Input.vue`)

- **Style:** `bg-background border border-border rounded-md px-3 py-2 text-sm`. Sits one step deeper than the surface, so the eye reads "this is where you type".
- **Label:** `text-xs font-medium text-foreground-muted uppercase tracking-wide` above the field. Required indicator is a single `*` in danger color.
- **Association:** every label uses `for` with a stable input `id`; hint and error text are connected with `aria-describedby`, and errors set `aria-invalid`.
- **Focus:** `focus:ring-1 focus:ring-white focus:border-white`. The ring is white, not primary — it's a "cursor's-here" marker, not an accent.
- **Optional leading icon** (Lucide): `pl-9`, icon at `absolute left-3 top-1/2 -translate-y-1/2 text-foreground-muted`.
- **Error:** error string in `text-xs text-danger` directly below the field.
- **Hint:** otherwise `text-xs text-foreground-muted` directly below.

### Status Badges (canonical: `Badge.vue`)

The codebase has two: `Badge.vue` (semantic-token-driven, canonical) and `StatusBadge.vue` (raw-Tailwind, legacy). Standardise on `Badge.vue`'s pattern.

- **Shape:** `rounded-full` (pill).
- **Variants:** default, primary, success, warning, error, info, gray. Each is `bg-{role}/20 text-{role} border border-{role}/30 font-medium`.
- **Sizes:** sm `px-2 py-0.5 text-xs`, md `px-2.5 py-1 text-sm`, lg `px-3 py-1.5 text-base`.
- **Optional dot:** prepends a small filled circle for "live" status indicators.

### Runtime Tags (`components/common/RuntimeTag.vue`)

The only place a function's runtime is drawn. Renders the Python or Node mark at
`w-3.5 h-3.5` in a desaturated identity tint (`--color-runtime-python`,
`--color-runtime-node`), with the versioned label ("Python 3.14", "Node.js 24")
carried in `title` and the accessible name. `withLabel` prints the label inline,
for pickers where the operator is choosing rather than recognising. An
unrecognised runtime prints its raw value rather than guessing a logo. See
section 7 for when a mark may replace a word at all.

The words themselves live in `utils/runtime.js`, not in this component. They
were previously spelled out here and again in the editor strip, while four other
places printed the API's raw value, so one function read "Node.js 24" on one
screen and "node" on the next. `<option>` cannot host a component, so the two
function-picker selects use `runtimeLabel()` directly -- that is the sanctioned
text path, and a unit test fails the build if any view interpolates a bare
`.runtime` again.

### Load Errors (`components/common/LoadError.vue`)

Shown when a list failed to load, above the list it replaces.

Every list view used to `catch (e) { console.error(e) }` and fall through to its
own empty state, so a failed request and an empty account rendered identically:
the operator was told "No API keys yet", "No channels yet", "No jobs yet" as
fact. Absence of data is not evidence of absence, and on the credential and
egress screens that false negative reads as reassurance.

Empty states are therefore gated on having actually loaded --
`v-if="loaded && !loadError && rows.length === 0"` -- and the failure gets a
`role="alert"` band naming what could not be loaded, the server's message, and a
Retry. A list that does not know what it holds must say so.

### Brand Marks (`components/icons/brand/`)

Single-path Simple Icons geometry, `fill="currentColor"`, `aria-hidden="true"`,
with a comment naming the source and the nominative use. Never full-colour vendor
logos: they are used to identify a runtime or an OAuth client, not to decorate,
and a saturated logo next to a status pill reads as a state.

### Brand Lockup (`components/layout/BrandLockup.vue`)

The mark plus the wordmark at one size, used by both the mobile top bar and the
sidebar drawer header. Both are on screen on a phone, so any difference between
them reads as the logo resizing while you navigate. Rendering both from one
component is what keeps that from recurring.

### Sidebar Navigation (`components/layout/Sidebar.vue`)

- **Width:** `w-64` mobile drawer, `lg:w-52` (208px) desktop inline.
- **Background:** `bg-background border-r border-border`. Same as page; only the right border separates it.
- **Brand block:** `h-16`, rendering `BrandLockup` — the same component the mobile top bar uses, so the mark cannot be one size in the bar and another in the drawer. Wordmark in `font-mono` (yes, mono — the brand wears its operator's clothes).
- **Items:** Overview, Chat, and Functions stay visible. Lower-frequency routes are grouped under collapsible Automation, Observe, and Connect disclosures; Settings and Docs remain direct links. Rows use `flex items-center gap-3 px-3 py-2.5 rounded-md text-sm font-medium` with distinct icons by silhouette.
- **Active:** `bg-primary text-white shadow-lg shadow-purple-900/20`. Hover: `text-white bg-surface-hover`.
- **Mobile:** the desktop sidebar transforms into an off-canvas drawer toggled from a `lg:hidden` top bar with a hamburger icon. The bar pads its **outer** element with `pt-safe` and puts the 56px row on an inner div, so the status-bar inset adds to the bar instead of eating it; page content clears it with `pt-topbar`, which is the same sum.

### Modals (`components/common/Modal.vue`) and Confirm Dialogs (`components/common/ConfirmDialog.vue`)

- **Shape:** `bg-background border border-border rounded-lg shadow-xl`. Header `border-b border-border`, footer `border-t border-border bg-surface/40`.
- **Backdrop:** `fixed inset-0 bg-black/60 backdrop-blur-sm`. Click-outside dismisses; Esc dismisses (handled at component level).
- **Sizes:** sm `max-w-sm`, md `max-w-lg` (default), lg `max-w-2xl`, xl `max-w-4xl`.
- **A11y:** `role="dialog"` + `aria-modal="true"` set; close button carries `aria-label`. **Focus trap not yet implemented**; tab still escapes to the page underneath. This is on the audit's P1 list.
- **Confirm Dialog:** narrower (`max-w-md`), leading icon (AlertTriangle for danger, HelpCircle for default), supports prompt mode (typed value capture) with auto-focused input.

### Drawer (`components/common/Drawer.vue`)

A side panel for inspector-style content (invocation request panel, activity row detail). Slides in from the right. Same surface and border treatment as Modal but full-height and right-anchored.

## 6. Do's and Don'ts

### Do

- **Do** keep page heads to one line above one body subhead, both standardised: `<h1 class="text-xl font-semibold text-white tracking-tight">` over `<p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-relaxed">`. Every dashboard view follows this; do not invent variants.
- **Do** route every status colour through `Badge.vue`'s variant system (`success / warning / error / info`). Status pills owned by `success/20` tints, status borders by `success/30`. Anything else forks the palette.
- **Do** use JetBrains Mono for any number or identifier the operator might want to compare against another. CPU cores, MB readings, latency, IDs, paths, ports, HTTP methods.
- **Do** promote section captions inside cards to real `<h2>` / `<h3>` while keeping the `text-xs font-bold uppercase tracking-wider` styling. Visual identity intact, semantics restored.
- **Do** reach for `bg-background border border-border` for cards-on-page and `bg-surface border border-border` for cards-on-deeper-surface. The system reads depth through borders + step shifts, not shadow.
- **Do** add `aria-label` to every icon-only button (delete, refresh, close, filter chips). Lucide icons announce nothing on their own.
- **Do** keep CTAs on roughly 5 to 10 percent of any screen. Most surfaces have one primary action; some have none.
- **Do** keep the decision-ready state visible and place setup forms, implementation metadata, and advanced detail behind native disclosure controls.

### Don't

PRODUCT.md names five anti-references. Each is below as a Don't.

- **Don't look like AWS Console.** No region selectors, no every-feature-on-screen surface, no low-density noise. Orva sits on one host; the UI should feel like one host. If a page wants a settings panel that lists ten unrelated knobs, push back: split it, or fold it into context where each knob lives.
- **Don't fall into the generic SaaS dashboard template.** No big-number-small-label-with-gradient-accent hero metrics. The shared design law spells this out as the "hero-metric template" ban; PRODUCT.md repeats the rule. Surface metrics on bars, sparklines, and stacked-bar viz, not on template-shaped tiles.
- **Don't turn onboarding into a product demo.** No gradients, feature panels, terminal theatre, or secondary education beside the account-creation task.
- **Don't apply cloud-vendor branding.** No clouds, no sky gradients, no "scale instantly" copy, no planet-scale rhetoric. Orva runs on one box you can touch.
- **Don't read as an AI-generated control plane.** Violet is an accent on graphite, not the atmosphere of every surface. No glowing borders, gradient text, animated mesh backgrounds, or decorative blurs.

The rest of the Don'ts apply across every register:

- **Don't** use em dashes in any user-facing string. Subheads, alerts, empty-state copy, toast messages: rewrite with periods, commas, colons, semicolons, or parentheses. Also no `--`. The templates are at zero and `responsive.test.js` now fails the build on a new one. Comments and copyable code samples keep theirs.
- **Don't** reach for raw Tailwind palette colours (`bg-blue-500/70`, `text-emerald-300`, `bg-amber-500/15`, `text-sky-300`) as a substitute for status meaning. Ten remain, all in `SourceTag.vue`, and they are deliberate: the source pill is a seven-way categorical identity axis, not a status axis, so it does not belong in the success/warning/danger families. Everywhere else uses the semantic tokens. An alpha variant of a token belongs in `color-mix(in srgb, var(--color-…) N%, transparent)`, not a hand-written `rgba()`.
- **Don't** hex-code colours inside a Vue component's scoped CSS. One remains, `#282c34` in `FunctionDiff.vue`, and it is exempt: it is `@codemirror/theme-one-dark`'s own editor background, and the file imports that theme, so the two have to agree. Everything else maps to `var(--color-…)`. `Firewall.vue` is the reference for the intended mapping.
- **Don't** use pure `#000` for surfaces or pure `#fff` for routine text. Use the graphite surface ladder and soft-gray foreground tokens; pure white is reserved for saturated action surfaces.
- **Don't** use `backdrop-blur` decoratively. The three glassmorphic icon chips on Onboarding are the exact pattern PRODUCT.md's Vercel/Railway anti-reference rejects. Blur is reserved for "the page underneath is no longer interactive".
- **Don't** lay out three or four identical icon-+-heading-+-paragraph cards in a feature grid. That template is the absolute ban "identical card grids" by name; PRODUCT.md flags the same shape under "Vercel / Railway / landing-page onboarding panels".
- **Don't** size `<button>` targets under 44×44 on coarse pointers. Grow the real box with `min-height` and `min-width`; invisible pseudo-element hit areas can overlap neighboring controls and steal clicks. The one exemption is WCAG 2.5.5's own: a control sitting inline inside a sentence, where a 44px box would set the line height of running prose. `KVStore.vue`'s inline "Load more" is the example, and it says so in place.
- **Don't** style section captions as `<div>`s and skip the heading. Screen readers cannot navigate to text-styled divs.
- **Don't** introduce `border-left-N` or `border-right-N` colored stripes on cards, list items, or alerts. The shared design law explicitly bans side-stripe borders.
- **Don't** animate layout properties (`width`, `height`, `top`, `padding`). Transforms and opacity only. Use `transition-colors`, `transition-transform`, `transition-opacity`. The codebase has zero `transition-all` today; keep it that way.
- **Don't** ease with bounce or elastic. Exponential ease-out (ease-out-quart / quint / expo) only.

---

## 7. Responsive & Adaptive

The dashboard had a complete set of adaptive primitives and no written rule for
applying them, so every view improvised and the phone experience drifted view by
view. This section is the rule. It describes what the code already does well, and
makes the parts that were assumed explicit.

### Breakpoints

Tailwind defaults, unmodified. Only three carry meaning here:

| Token | Width | What changes |
|---|---|---|
| `sm` | 640px | Mobile card list gives way to the desktop table. |
| `md` | 768px | Two-pane layouts split (editor, AI rail). |
| `lg` | 1024px | Sidebar stops being a drawer and becomes inline. |

`xl` (1280px) is for progressive column reveal only. Do not introduce `2xl`.
Target device classes: phone 375–430, tablet 768–834, laptop 1280–1440, desktop 1920.

### The root is scaled

`html { font-size: 95% }`, so **1rem = 15.2px**. Every rem-based size is 5% under
its nominal Tailwind value: `h-10` is 38px, not 40; `text-base` is 15.2px, not 16.
Reason about real pixels whenever a threshold matters. Two rules exist because of
this scale, and both were broken before it was written down:

- A control is only touch-compliant at `h-11` (41.8px) or above, and only `h-12`
  (45.6px) clears 44px unaided. Everything smaller carries a `touch-expand-*`,
  which floors both height **and** width: a `Button` whose slot holds only an
  icon measured 35px wide while measuring a compliant 44px tall. A control that
  is natively small and has no clickable label beside it (a bare checkbox in a
  card whose body is already a button) gets wrapped in a padded `<label>` that
  carries the target instead, so the box stays dense and the hit area does not.
- `text-base` does **not** clear iOS Safari's 16px focus-zoom threshold. Do not
  reach for it to stop the zoom; `style.css` pins form controls to a real 16px
  under `@media (pointer: coarse)` instead, once, globally.

### The overflow contract

`html, body { overflow-x: hidden }` is deliberate: the document never scrolls
sideways. The cost is that **an overflowing container is clipped silently, with no
scrollbar to reveal it.** So every container that can exceed its width must own its
own scrolling:

```html
<div class="overflow-x-auto scrollable"> … </div>
```

Never put `overflow: hidden` on something that holds content. Use it only to clip
a rounded corner, and only where the content provably fits. A wide table, a code
block, a fixed-track grid and a long unbroken identifier are all content.

### The list-page contract

Every collection view renders **both** branches, in one bordered card:

```html
<div class="bg-background border border-border rounded-lg overflow-x-auto">
  <ul class="sm:hidden divide-y divide-border"> … stacked cards … </ul>
  <table class="hidden sm:table w-full text-sm text-left"> … </table>
</div>
```

Cells are `px-6 py-4`, header cells `px-6 py-3`. The text column is
`min-w-0 flex-1`, the action cluster `shrink-0`.

**Parity is the rule, not the aspiration.** Any action, badge or identifier
reachable from the desktop row must be reachable from the mobile card. Hiding a
column at a breakpoint is progressive disclosure; dropping an action is removing a
feature from anyone holding a phone. Where the desktop table reveals columns
across `sm`/`md`/`lg`/`xl`, the mobile card carries that same information in its
meta rows.

Rows open detail views through a real focusable control. A `@click` on a bare
`<li>` or `<tr>` is not keyboard-operable and fails WCAG 2.1.1 at Level A.

### Viewport units

`dvh`, never `vh`, for anything sized to the viewport. On mobile browsers `100vh`
is the *large* viewport, so a `100vh` shell extends behind the retracting URL bar
and the bottom of every scroll container hides under it. `dvh` also shrinks when
the on-screen keyboard opens, which is what keeps a focused field visible without
a `visualViewport` listener.

### Physical edges

Fixed elements against a screen edge pad with the safe-area utilities
(`pt-safe`, `pb-safe`, `pl-safe`, `pr-safe`, `p-safe`, `pb-composer`). Pad the
**outer** element and let an inner element carry the fixed height: putting `h-14`
and `pt-safe` on one box makes the inset eat the row instead of adding to it.
Content offset from the fixed mobile top bar uses `pt-topbar`, which is that bar's
height plus the inset, so the two cannot drift apart.

### Data visualisation on small screens

Bars and tracks thicken on phones (`h-2 sm:h-1.5`): a 6px rule is legible on a
monitor at arm's length and close to invisible in the hand. Fills animate with
`transform: scaleX()` from `origin-left`, never by animating `width`, which
relayouts the row on every poll. Any SVG drawn with `preserveAspectRatio="none"`
must set `vector-effect="non-scaling-stroke"`, or the same line renders at a
different weight in a narrow card than a wide one. Every chart carries `role="img"`
and an `aria-label` stating the actual values, because a bar has no text to read.

### Icons and text

Icons resolve faster than words, but only when the reader already knows the mark.
Choose by what the reader is doing, not by what looks cleaner:

- **Icon alone** when the set is small and stable, the mark is already known, and
  the label adds nothing: the runtime in a list row (`RuntimeTag`), a row action
  (`IconButton`). The word moves to `title` and the accessible name; it is never
  simply deleted.
- **Icon with text** when the reader is *choosing* rather than recognising (a
  runtime picker, a settings toggle), or when the concept has no established mark
  and the icon is only a scanning aid (the activity source pill).
- **Text alone** for anything with more than a handful of values, for numbers and
  measurements, and for HTTP methods, which are already their own vocabulary.
- **Never icon alone for status.** Outcome must survive both colour-blindness and
  an unfamiliar glyph, so a status carries its word, its token colour, and its
  shape together. `StatusBadge` is the canonical example and is not to be reduced.

### Enforcement

`frontend/test/responsive.test.js` asserts the mechanical half of this section and
runs in CI via `npm test`. The half it cannot assert is geometry, which has to be
measured in a real browser: the source says `h-10`, only a render says 38px. Every
number in this section was measured that way, across the shipped route set at
375/390/430/768/820/1280/1920 with `pointer: coarse` emulated on the phone widths. It is there because every rule above was already true in
at least one view and false in another; a convention nothing checks is a
convention that decays.
