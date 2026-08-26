# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary user is a technical operator running Orva on hardware they own: a
homelabber, indie developer, or small on-premises team. They are comfortable
with shells, JSON, and HTTP APIs, and usually operate one Orva instance from a
single screen.

Their core loop is to write a function, deploy it, invoke it, inspect failures,
schedule work, and persist small amounts of state without depending on a cloud
control plane.

## Product Purpose

Orva is a self-hosted Function-as-a-Service for homelab and on-premises use. It
deploys JavaScript, TypeScript, and Python functions into nsjail sandboxes and
exposes them over HTTP through a built-in dashboard, CLI, MCP server, and AI
assistant.

Success means an operator can bring up one instance and complete both the first
deploy and routine operations without assembling separate control-plane
services or leaving Orva for common day-two tasks.

## Positioning

Orva provides a complete single-host function platform rather than a remote
cloud account or a collection of loosely connected self-hosted tools. The same
instance owns sandboxed execution, warm pools, deployment history, schedules,
jobs, KV state, webhooks, egress policy, observability, CLI access, MCP tools,
and an AI operator.

## Operating Context

Orva runs in homelabs and on-premises environments where operators value local
ownership, predictable behavior, and direct access to their infrastructure.
They move between the web dashboard, terminal, CI, external HTTP clients, and
AI/MCP clients. The dashboard is an operating console, not a marketing surface.

Most sessions are short and task-oriented: deploy or edit one function, inspect
an execution, respond to a failure, or adjust one integration. The interface
must make the next operational action obvious without requiring the operator to
re-read platform explanations on every visit.

## Capabilities and Constraints

- The supported runtimes are Node.js 24, Python 3.14, and TypeScript compiled
  into the Node runtime. Orva deliberately supports latest-stable runtimes only.
- Linux function execution is isolated with nsjail. Egress policy is per
  sandbox and fail-closed.
- The server is distributed as a single binary with the dashboard embedded.
- Server configuration is environment-variable based. CLI configuration is
  stored separately in the operator's home directory.
- Orva is a single-instance operator product. Multi-tenant administration and
  fleet-management abstractions are not product goals.
- Both a day and a night theme are supported, and the choice belongs to the
  operator. The dashboard follows the operating system by default; an explicit
  choice is remembered per browser. Both themes are warm neutrals at the same
  hue family, so switching does not change the product's temperature, only its
  lightness. The code editor is deliberately dark in both, the way a terminal
  is: it is an instrument rather than a page.
- Existing functionality must remain accessible while the interface uses
  progressive disclosure to keep routine tasks focused.

## Brand Commitments

Orva is operator-grade, calm, and technical. Its voice is dry, pragmatic, and
direct. It assumes competence, uses plain operational language, and avoids
marketing copy. Inter carries prose; JetBrains Mono is reserved for code, paths,
identifiers, and comparable measurements.

The interface must not resemble a cloud-vendor console, a generic SaaS metric
dashboard, or a decorative AI-generated control plane. Violet is the existing
identity accent, but restraint, hierarchy, and product-specific content keep it
from becoming ornamental.

## Evidence on Hand

- The repository contains the complete runnable product, including the Vue
  dashboard, Go server, CLI, MCP server, runtime adapters, and automated tests.
- `CONTRACT.md` is the canonical operational and release contract.
- `docs/reference.md` is the canonical user-facing product reference.
- `DESIGN.md` records the incumbent visual system and its constraints.
- No testimonials, customer logos, usage claims, or external validation assets
  are supplied. Future work must not fabricate them.

## Product Principles

1. **Operational clarity.** The current task and next action should be obvious.
   Explain a concept once, at the point where the information changes a
   decision.
2. **Secure defaults.** Isolation, authentication, secrets, and egress controls
   should fail safely without turning every screen into a security lecture.
3. **Low cognitive load.** Preserve complete capability while hiding advanced
   detail until it is relevant. Use headings and spacing before adding boxes.
4. **Local ownership.** Core workflows must remain self-hosted and usable
   without a hosted control plane or third-party design dependency.
5. **Operator trust.** Show real state, precise errors, and recoverable actions.
   Avoid decorative data, invented proof, and interface theatrics.

## Accessibility & Inclusion

Target WCAG 2.1 AA across the dashboard:

- Every interactive control is keyboard reachable with a visible focus state.
- Labels, hints, errors, and icon-only actions expose programmatic names and
  relationships.
- Status never relies on color alone.
- Heading hierarchy uses semantic `h1`, `h2`, and `h3` elements.
- Touch targets meet a 44 by 44 pixel effective area on mobile.
- Reduced-motion preferences preserve understandable state changes without
  layout animation.
