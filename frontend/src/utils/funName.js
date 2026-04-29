// Function name generator — adjective + noun, hyphenated.
//
// Used to seed the Editor's name field when the operator opens a fresh
// "Deploy" page so they don't stare at a blank input. The user can
// always edit the field, and a re-roll button mints a new pair.
//
// Word picks favour evocative, lowercase, single-token, alphanumeric
// strings so the result is always a valid function name (matches the
// backend's /^[a-z][a-z0-9-]{0,62}$/ rule). Curated to skip anything
// negative, ambiguous, or hard to pronounce.

const adjectives = [
  'amber', 'arctic', 'aurora', 'bold', 'brave', 'breezy', 'bright',
  'brisk', 'calm', 'celestial', 'cobalt', 'cosmic', 'crimson', 'crisp',
  'crystal', 'dapper', 'dazzling', 'deep', 'eager', 'ember', 'fearless',
  'feisty', 'fierce', 'flaming', 'fluent', 'fluorescent', 'frosty',
  'gentle', 'glacial', 'golden', 'graceful', 'happy', 'hazy', 'icy',
  'indigo', 'jade', 'jolly', 'jovial', 'keen', 'kindred', 'lavender',
  'lively', 'lucent', 'lunar', 'magenta', 'magnetic', 'merry', 'midnight',
  'mighty', 'mellow', 'mossy', 'mystic', 'neon', 'nimble', 'noble',
  'obsidian', 'opal', 'pearl', 'peppy', 'pixel', 'plucky', 'plush',
  'polar', 'prime', 'quartz', 'quick', 'quiet', 'radiant', 'rapid',
  'rare', 'roaming', 'rosy', 'royal', 'rugged', 'runic', 'rustic',
  'sapphire', 'scarlet', 'sharp', 'silent', 'silken', 'silver', 'sleek',
  'smooth', 'snowy', 'snug', 'solar', 'sonic', 'spry', 'starlit',
  'stellar', 'sturdy', 'sublime', 'sunny', 'svelte', 'swift', 'tame',
  'tender', 'thunder', 'tidal', 'topaz', 'tropic', 'turquoise', 'twilight',
  'urban', 'velvet', 'verdant', 'vibrant', 'violet', 'vivid', 'warm',
  'whisper', 'wild', 'wise', 'witty', 'woven', 'zesty', 'zen',
]

const nouns = [
  'albatross', 'amber', 'antler', 'apricot', 'archer', 'arrow', 'atlas',
  'aurora', 'badger', 'bayou', 'beacon', 'bison', 'blossom', 'bramble',
  'breeze', 'cactus', 'canyon', 'caravan', 'cedar', 'cliff', 'comet',
  'compass', 'coral', 'cosmos', 'cypress', 'dawn', 'delta', 'dolphin',
  'drift', 'dune', 'eagle', 'ember', 'fable', 'falcon', 'fern', 'fjord',
  'flame', 'flint', 'forest', 'galaxy', 'garnet', 'geyser', 'glacier',
  'glade', 'glint', 'gorge', 'gull', 'harbor', 'haven', 'horizon',
  'iceberg', 'iris', 'jaguar', 'jetty', 'jungle', 'kelp', 'kestrel',
  'kettle', 'kraken', 'lagoon', 'lantern', 'lark', 'ledge', 'lily',
  'lighthouse', 'lupine', 'lynx', 'maple', 'meadow', 'meridian', 'meteor',
  'mirage', 'mistral', 'monsoon', 'moon', 'moss', 'mountain', 'nebula',
  'oak', 'oasis', 'ocean', 'orchid', 'osprey', 'otter', 'panda', 'panther',
  'parrot', 'pebble', 'phoenix', 'pine', 'pinion', 'pixel', 'planet',
  'plume', 'pond', 'poppy', 'prairie', 'puffin', 'puma', 'quartz',
  'quasar', 'quill', 'rapids', 'raven', 'reef', 'ridge', 'river', 'robin',
  'rune', 'sage', 'satellite', 'savanna', 'sequoia', 'shadow', 'signal',
  'silo', 'sky', 'sloth', 'snow', 'sparrow', 'spire', 'star', 'stream',
  'summit', 'swan', 'tempest', 'thicket', 'thistle', 'thunder', 'tide',
  'tiger', 'totem', 'tower', 'tundra', 'twilight', 'twister', 'valley',
  'vortex', 'walnut', 'wave', 'whale', 'whisper', 'wildflower', 'willow',
  'wolf', 'wren', 'zenith', 'zephyr',
]

const pick = (arr) => arr[Math.floor(Math.random() * arr.length)]

// generateFunctionName returns a hyphenated adjective-noun pair. Always
// lowercase, never longer than 30 characters. Idempotent in shape; the
// caller is welcome to keep re-rolling until they like one.
export function generateFunctionName() {
  return `${pick(adjectives)}-${pick(nouns)}`
}
