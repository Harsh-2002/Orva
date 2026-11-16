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

THESIS: Make each permission's effect and scope inspectable before saving.
Reject a dense action matrix that hides selectors or loses mobile parity.

OWN-WORLD: Preserve Orva's current warm day/night tokens, Inter and purposeful
mono identifiers, semantic statuses and existing controls. No new palette.

STORY: Read enforcement/review status, inspect outbound rights and inbound
callers, edit an independent draft, then inspect changes before an explicit save.
Unknown capabilities deny edits. Empty grants do not imply an unavailable SDK.

FIRST VIEWPORT: Function identity and a compact state line precede outbound and
inbound sections. Human effects lead; UUIDs remain inspectable. Review and bounded
history follow. A dirty-only save bar retains the same actions on phone/desktop.

FORM: Sectioned authority sheet chosen through delegated expert review.
Surface exploration reference: seed:831b72e0fd427e2f. Existing navigation and controls
remain authoritative; no catalog visual-world replacement is adopted.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Integration limits

Build against an internal, validated view-model seam, not an invented REST route.
No router/Editor/SDK-toggle changes until server-authoritative contracts and
cut-over enforcement exist. Preview fixtures must be clearly synthetic and cannot
claim permissions were applied. Policy never joins source/deployment dirty state.
No production or website deployment.
