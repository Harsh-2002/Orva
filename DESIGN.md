---
name: Orva
description: Self-hosted Function-as-a-Service for homelab operators and on-prem teams.
colors:
  background: "#12111C"
  surface: "#1A1929"
  surface-hover: "#252438"
  border: "#2D2B42"
  foreground: "#FFFFFF"
  foreground-muted: "#A3A3B3"
  primary: "#553F83"
  primary-hover: "#684D9E"
  primary-foreground: "#FFFFFF"
  secondary: "#2D2B42"
  secondary-hover: "#3E3B5A"
  danger: "#EF4444"
  warning: "#EAB308"
  success: "#22C55E"
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

The palette is the deep purple-tinted near-black of an editor at midnight, paired with a single muted violet accent that carries every CTA. JetBrains Mono runs alongside Inter so paths, IDs, byte counts, and CPU readings sit in their natural register without arguing for attention. Rhythm is built from quiet density: small type, generous gaps where ideas separate, tight gaps within a single thought, no empty-space-as-decoration. Surfaces are flat. Shadows whisper, never advertise.

What this system rejects: the SaaS-marketing dashboard family (gradient hero panels, three-up icon-feature cards, glassy decorative blurs), category clichés (cloud-vendor purple gradients, "modern" minimal-cream-with-emerald, observability navy), and copy that sounds written rather than spoken. Orva is operator software for people who chose to self-host, not a sales pitch for becoming a self-hoster.

**Key Characteristics:**
- Deep violet-tinted dark surfaces; one muted purple accent carries identity.
- Inter for prose, JetBrains Mono for any data the operator might compare digit-to-digit.
- Standardised page heads: `text-xl semibold tracking-tight` over a one-line subhead in muted text, max-w-prose.
- Hidden scrollbars by intent: nested overflow areas (modals, drawers, code blocks) feel calmer without 6px tracks in every region.
- Status colour is semantic, never decorative. A pill that's amber means in-flight; an amber accent that means nothing is forbidden.
- No copy uses em dashes. Periods, commas, colons, semicolons, parentheses. The voice is operator-spoken.

## 2. Colors

A single muted violet accent on a deep violet-tinted near-black, with cool gray-violet text and three semantic status colours. The neutrals all carry a faint hue tilt toward the brand violet so nothing in the UI looks plastic-gray.

### Primary
- **Muted Violet** (`#553F83`, ≈`oklch(40% 0.10 295)`): the only identity colour. Carries primary CTAs (Deploy, Save, Confirm), the active sidebar item, status-running indicators, and selected chip filters. Reserved enough that when it appears, the eye knows where to land.
- **Muted Violet Hover** (`#684D9E`, ≈`oklch(48% 0.12 295)`): hover state for primary surfaces. Lightness step only; same hue and chroma family.

### Neutral
- **Background** (`#12111C`, ≈`oklch(13% 0.029 277)`): page surface and the body of cards. Almost black, faintly violet so it never reads as a flat #0d0d0d void.
- **Surface** (`#1A1929`, ≈`oklch(18% 0.035 277)`): one elevation step up. Used for headers/footers inside cards, modal backdrops behind content rows, and inline code blocks.
- **Surface Hover** (`#252438`, ≈`oklch(24% 0.042 277)`): hover state for rows, list items, and ghost buttons. The first frame of "I noticed you".
- **Border** (`#2D2B42`, ≈`oklch(28% 0.045 277)`): every divider, every card outline, every input stroke. The system reads layers through borders, not shadows.
- **Foreground** (`#FFFFFF`): currently pure white. **Tint to `#F4F2FA` (≈`oklch(96% 0.01 290)`)** to match the rest of the palette's hue discipline; the eye reads pure white as harsh against a violet-tinted dark. The `--color-foreground-strong: #FFFFFF` token is reserved for text on saturated brand-colour surfaces (primary CTA labels, on-violet badges); the default `--color-foreground` may shift to a tinted off-white in a future palette pass without breaking those high-contrast sites.
- **Foreground Muted** (`#A3A3B3`, ≈`oklch(70% 0.018 280)`): all secondary text, icon defaults, table cell content one rank below primary. The cool gray-violet is intentional.

### Status (semantic, used at low chroma)
- **Success** (`#22C55E`, ≈`oklch(73% 0.21 144)`): only for terminal-success states. Pair with the recommended `success/15` tint background and `success/30` border (see Named Rule below).
- **Warning** (`#EAB308`, ≈`oklch(78% 0.16 86)`): in-flight, queued, soft warnings ("expires in 7 days"), drift hints.
- **Danger** (`#EF4444`, ≈`oklch(64% 0.23 25)`): destructive actions, failed states, error toasts.

### Named Rules

**The One Accent Rule.** Muted violet appears on roughly 5 to 10 percent of any given screen. Primary CTAs, the active sidebar item, the selected filter chip. Anywhere else, ask whether the page actually needs an accent or whether a border + foreground-muted will read more confidently. Restraint is the point.

**The Tinted Neutral Rule.** Pure `#000` and `#fff` are forbidden. Every neutral carries the violet hue with chroma in the 0.005 to 0.045 band. The background is not "dark gray", it is dark violet at low chroma; the muted text is not "neutral gray", it is cool gray-violet. The forensic eye sees this; the casual eye feels it.

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

**Sizes:** xs `h-7 px-2.5` (28px), sm `h-8 px-3` (32px), md `h-10 px-4` (40px, default), lg `h-12 px-6` (48px). The 28px / 32px sizes need their *hit area* (not visible height) extended to 44px for mobile touch — see the Touch-Target Rule below.

### Filter Chips

Filter pills on `Jobs.vue`, `Webhooks.vue`, `CronJobs.vue` status strips. Same component as Button, `variant="chip"`.

- **At rest:** `bg-surface text-foreground-muted border border-border`.
- **Hover:** `text-white border-foreground-muted`.
- **Active:** `bg-primary text-primary-foreground border-primary`. No shadow — chips sit flat on the surface, unlike CTAs which lift.

### Cards

Two parallel conventions exist in the codebase; document both honestly.

- **Page-Level Card** (used in `Dashboard.vue` for tiles, `Activity.vue` for stat strips, etc.): `bg-background border border-border rounded-lg p-5`. The page itself is `bg-surface`-shaped (via the layout shell), so cards are a step *down* into the deep background. Headline group uses `text-xs font-bold uppercase tracking-wider` followed by `text-[11px] text-foreground-muted` hint copy.
- **Component Card** (`components/common/Card.vue`): `bg-surface border border-border rounded-lg`, optional header/footer slots with `border-b/t border-border` dividers and a `bg-surface/50` footer tint. Hoverable variant adds `hover:border-foreground-muted`.

**Padding scale:** `none / sm: p-4 / normal: px-6 py-4 / lg: p-8`. Dashboard tiles use `p-5` (20px) inline, which is the rhythm step between sm and normal — keep.

### Inputs (`components/common/Input.vue`)

- **Style:** `bg-background border border-border rounded-md px-3 py-2 text-sm`. Sits one step deeper than the surface, so the eye reads "this is where you type".
- **Label:** `text-xs font-medium text-foreground-muted uppercase tracking-wide` above the field. Required indicator is a single `*` in danger color.
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

### Sidebar Navigation (`components/layout/Sidebar.vue`)

- **Width:** `w-64` mobile drawer, `lg:w-52` (208px) desktop inline.
- **Background:** `bg-background border-r border-border`. Same as page; only the right border separates it.
- **Brand block:** `h-16` with the Orva mark + wordmark in `font-mono` (yes, mono — the brand wears its operator's clothes).
- **Items:** `flex items-center gap-3 px-3 py-2.5 rounded-md text-sm font-medium`. Single-word labels, distinct icons by silhouette (Gauge, Boxes, CalendarClock, ListChecks, Activity, ListTree, Network, Fingerprint, Plug, Webhook, ShieldHalf, Settings, LibraryBig).
- **Active:** `bg-primary text-white shadow-lg shadow-purple-900/20`. Hover: `text-white bg-surface-hover`.
- **Mobile:** the desktop sidebar transforms into an off-canvas drawer toggled from a `lg:hidden` top bar with a hamburger icon.

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

### Don't

PRODUCT.md names five anti-references. Each is below as a Don't.

- **Don't look like AWS Console.** No region selectors, no every-feature-on-screen surface, no low-density noise. Orva sits on one host; the UI should feel like one host. If a page wants a settings panel that lists ten unrelated knobs, push back: split it, or fold it into context where each knob lives.
- **Don't fall into the generic SaaS dashboard template.** No big-number-small-label-with-gradient-accent hero metrics. The shared design law spells this out as the "hero-metric template" ban; PRODUCT.md repeats the rule. Surface metrics on bars, sparklines, and stacked-bar viz, not on template-shaped tiles.
- **Don't lift Vercel / Railway / landing-page onboarding panels.** Diagonal gradient backgrounds, decorative blurred circles, three identical glassmorphic feature chips with icon + heading + short description. The `Onboarding.vue:4` panel is exactly this template today; replace it with a register native to Orva (live terminal output, real curl→response trace, an editor preview of a deployed function).
- **Don't apply cloud-vendor branding.** No clouds, no sky gradients, no "scale instantly" copy, no planet-scale rhetoric. Orva runs on one box you can touch.
- **Don't read as an AI-generated control plane.** The codebase already uses violet on near-black, so the discipline that keeps it from reading like that template comes from the rest of these rules. Specifically: no glowing borders, no gradient text, no animated mesh backgrounds, no decorative blurs.

The rest of the Don'ts apply across every register:

- **Don't** use em dashes in any user-facing string. Subheads, alerts, empty-state copy, toast messages: rewrite with periods, commas, colons, semicolons, or parentheses. Also no `--`. The recent header standardisation pass left ≈16 in template bodies and more in JS-built strings; sweep them.
- **Don't** reach for raw Tailwind palette colours (`bg-blue-500/70`, `text-emerald-300`, `bg-amber-500/15`, `text-sky-300`) as a substitute for status meaning. The codebase has 125 of these and every one is a pending palette migration.
- **Don't** hex-code colours inside a Vue component's scoped CSS. `Firewall.vue:900–902` and `Docs.vue` carry hex literals; map them to `var(--color-…)` so a future theme change works.
- **Don't** use `#000` or `#fff`. Foreground should tint toward the brand violet (target `#F4F2FA`); background and surfaces are already correctly tinted.
- **Don't** use `backdrop-blur` decoratively. The three glassmorphic icon chips on Onboarding are the exact pattern PRODUCT.md's Vercel/Railway anti-reference rejects. Blur is reserved for "the page underneath is no longer interactive".
- **Don't** lay out three or four identical icon-+-heading-+-paragraph cards in a feature grid. That template is the absolute ban "identical card grids" by name; PRODUCT.md flags the same shape under "Vercel / Railway / landing-page onboarding panels".
- **Don't** size `<button>` hit areas under 44×44 on mobile. The `xs` (h-7 = 28px) and `sm` (h-8 = 32px) Button sizes carry meaningful hit-area gaps; extend the click target with padding or a `::before` overlay if the visual height matters.
- **Don't** style section captions as `<div>`s and skip the heading. Screen readers cannot navigate to text-styled divs.
- **Don't** introduce `border-left-N` or `border-right-N` colored stripes on cards, list items, or alerts. The shared design law explicitly bans side-stripe borders.
- **Don't** animate layout properties (`width`, `height`, `top`, `padding`). Transforms and opacity only. Use `transition-colors`, `transition-transform`, `transition-opacity`. The codebase has zero `transition-all` today; keep it that way.
- **Don't** ease with bounce or elastic. Exponential ease-out (ease-out-quart / quint / expo) only.
