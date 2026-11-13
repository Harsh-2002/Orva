const { posix } = require('node:path');

exports.message = name => `Hello ${posix.basename(name)}`;
