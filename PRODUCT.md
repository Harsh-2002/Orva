# Product

## Register

product

## Users

A single technical operator running Orva on hardware they own: homelabbers, indie developers, small self-hosting teams. Most often working alone, after hours, on a single monitor in a dim room. They are comfortable with shell, JSON, and curl, and they expect a control plane that respects that competence rather than abstracting it away.

The job to be done is the same loop they would otherwise pay a cloud provider for: write a function, deploy it, invoke it from outside, debug when it breaks, schedule it, and persist a little bit of state. Orva replaces the cloud account, not the function. The user already knows what serverless is; they want it on their own box, with the warm-pool latency and dashboards intact.

## Product Purpose

Orva is a self-hosted Function-as-a-Service for homelab and on-premises use. It deploys JavaScript (Node 22/24), Python (3.13/3.14), and TypeScript functions into nsjail sandboxes and exposes them over HTTP, with a built-in dashboard, CLI, and MCP server.

Success looks like this: an operator brings up the container, writes a function in the editor, hits Deploy, and the first invocation lands in single-digit milliseconds. They never have to leave the dashboard for the day-2 surface, jobs, cron, secrets, KV, webhooks, firewall, traces. The control plane feels like a serious piece of infrastructure they actually want to keep running, not a hobby project they tolerate.

## Brand Personality

Three words: **operator-grade, calm, technical.**

The voice is the voice of someone who has run a function platform at scale and is now building one to keep at home. Dry, pragmatic, slightly opinionated. Empty states explain what something is and what it will look like once it has data, not a sales pitch for the feature. Section captions are short, declarative, and do not introduce themselves. Mono font carries data; sans carries prose; nothing carries marketing.

Confidence without polish. Linear, Railway, Fly, Tailscale admin: that family. Looks like a tool that an engineer would build for themselves, then realised would also work for the next operator over.

## Anti-references

What Orva must not look or feel like:

- **AWS Console.** Every-feature-on-screen, region selectors, low information density per pixel despite the noise. Orva sits on one host; the UI should feel like one host.
- **Generic SaaS dashboard with hero-metric tiles.** Big number, small label, supporting stats, gradient accent under the value. Orva surfaces metrics, but they belong on bars and sparklines, not in template-shaped tiles.
- **Vercel / Railway / landing-page onboarding panels** with diagonal gradient backgrounds, decorative blurred circles, and three identical glassmorphic feature chips (icon + heading + short description, repeated). The onboarding view today still leans on this template; PRODUCT.md keeps the rule even after it is fixed.
- **Cloud-vendor branding.** Clouds, sky gradients, "scale instantly" copy, planet-scale anything. Orva runs on one box you can touch.
- **AI-generated control planes.** Purple-on-near-black with violet accents, glowing borders, gradient text headings, animated mesh backgrounds. Orva is dark and uses violet, but the discipline that keeps it from reading as that template comes from the rest of these anti-references.

## Design Principles

1. **Operator over operator-of-operators.** Built for the person who runs Orva, not for the team that manages a fleet of operators. No multi-tenant shapes, no role-based abstraction layers in the UI surface. One operator, one console.

2. **Density without noise.** Pack signal into every row. A long table beats a paginated card grid. Hidden scrollbars and tight line-height are intentional, not accidents to be fixed.

3. **Practice what you preach.** The dashboard runs on the same self-hosted, single-binary ethos that Orva sells. The UI should not require a service the platform itself does not run.

4. **Show, don't tell.** Empty states show what the data will look like once it exists (a sample row, a real curl example, a trace shape) rather than marketing copy or icon illustrations.

5. **Confidence without polish.** Skip the demo flourishes (gradient backgrounds, decorative blurs, animated heroes). The platform earns trust by looking like the kind of tool the user could have written themselves, not by impressing them.

## Accessibility & Inclusion

Target: WCAG 2.1 AA across the dashboard. Specific commitments:

- `prefers-reduced-motion` respected; transitions are short (≤150ms) and limited to color and opacity, never layout properties.
- Every interactive control reachable by keyboard. Focus rings visible against the dark surface. Modal and drawer dialogs implement focus trap and restore.
- Icon-only buttons carry `aria-label`. Status badges use color plus glyph or text, never colour alone.
- Heading hierarchy is real (`<h1>` → `<h2>` → `<h3>`), not styled `<div>`s, so screen-reader navigation works.
- Touch targets meet 44×44 on mobile breakpoints, including filter chips and icon controls.
- Dark theme is the default and the only theme. A light theme is not a roadmap commitment; the audience and use case both lean dark, and a half-supported toggle is worse than none. Revisit if real user demand emerges.
