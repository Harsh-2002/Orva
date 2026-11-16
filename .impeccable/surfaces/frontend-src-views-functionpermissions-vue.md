---
version: 1
slug: "frontend-src-views-functionpermissions-vue"
primary_target: "frontend/src/views/FunctionPermissions.vue"
related_targets: []
---

# Function permissions

Mode: Operate. User chose a dedicated permissions page and delegated UI decisions
to subagents. This extends Orva's existing design; it does not replace it.

## Direction contract

THESIS: A first-time operator can see current access and build a policy without
understanding its JSON schema. The user rejected the original authority sheet
as confusing and explicitly chose an AWS-style guided builder.

OWN-WORLD: Preserve Orva's current warm day/night tokens, Inter and purposeful
mono identifiers, semantic statuses and existing controls. No new palette.

STORY: Inspect current stored access, choose Edit/Create, select a service,
choose actions, scope resources, add the permission, then review and save the
whole draft. Current access and unsaved changes never share an ambiguous label.

FIRST VIEWPORT: Function identity, concise state and current-access summaries
lead to one primary Edit/Create action. Editing reveals a numbered guided path,
not all forms at once. History and canonical JSON are secondary disclosures.

FORM: User-confirmed guided builder: service → actions → resources → review.
seed: none (pinned). User: “AWS like IAM policy maker”; structured confirmation:
“Guided builder (Recommended)”. Existing Orva tokens and primitives remain fixed.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Integration limits

Build against an internal, validated view-model seam, not an invented REST route.
No router/Editor/SDK-toggle changes until server-authoritative contracts and
cut-over enforcement exist. Preview fixtures must be clearly synthetic and cannot
claim permissions were applied. Policy never joins source/deployment dirty state.
No production or website deployment.

## Rendered finish review (2026-11-16)

The superseded authority-sheet implementation received an independent visual
review finding no blocking design issues in actual
night/day phone, tablet and desktop captures, including dirty diffs and review
disclosures. The repeated selector-kind word in diff text was corrected.
Parent browser checks verified bottom History reachability with the expanded
save bar at 375px in both themes. Optional caller-field width refinement is
deferred; unresolved-target wording must not invent visibility/deletion facts.

Forty synthetic render cases passed without horizontal overflow or browser
errors. This is not complete accessibility or production Layout/auth acceptance.
Independent behavioral re-review passed after correcting stale adoption and
delayed-conflict ordering: 30 unchanged probes and four new ordering cases.
Acceptance assumes the future controller's documented response correlation
contract, not production integration. No raster assets
were added to the shipping UI; evidence PNGs are browser screenshots.

The user's subsequent hands-on rejection overrides that visual verdict.
The guided replacement requires fresh first-use journey and rendered review;
the earlier acceptance is behavioral evidence for preserved guards, not UX
acceptance of the replacement.

## Guided replacement verification

Fresh first-use review required three material corrections: compact editing
context and task-focused scrolling, accurate stored-versus-draft copy, and no
Done exit while a permission is half-filled. All three were scored resolved
after recapture. At 375px the service choices and Next fit together; desktop
actions and Next fit in one frame. Quarantine and conflict warnings remain.

The corrected staged workflow passed 67 independent acceptance checks, 105 Node
and 211 repository component tests, explicit-page build and browser journeys.
Forty-eight rendered states covered both themes at 375/768/1440px; negative
browser checks confirmed deliberate scope changes and unsupported-target denial.
The old defect-expecting diagnostic witness remains recorded as expected red.
No new palette or raster assets ship. This is not live IAM/API/CLI/MCP or complete
accessibility acceptance; the future controller's correlation contract remains.
