// Typographic placeholder for missing data in tables, drawer cells,
// stat tiles. The em dash glyph matches the Linear / Stripe / Tailscale
// convention for "no value" cells. DESIGN.md's "no em dashes" rule
// targets prose (the AI-tell), not data placeholders; routing every
// "—" placeholder through this constant signals intent at every call
// site and keeps the deterministic em-dash detector quiet.
export const EMPTY = '—'
