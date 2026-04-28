// Realistic REST API handler — parses, validates, transforms data.
module.exports.handler = async function (event) {
  const body = typeof event.body === 'string' ? JSON.parse(event.body || '{}') : event.body || {};

  // Validate required fields
  const errors = [];
  if (!body.name || typeof body.name !== 'string') errors.push('name is required');
  if (body.email && !body.email.includes('@')) errors.push('invalid email');

  if (errors.length > 0) {
    return {
      statusCode: 400,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ errors }),
    };
  }

  // Transform data
  const user = {
    id: 'usr_' + Date.now().toString(36) + Math.random().toString(36).slice(2, 8),
    name: (body.name || 'anonymous').trim().toLowerCase(),
    email: (body.email || '').toLowerCase(),
    tags: (body.tags || []).map(t => t.toLowerCase().trim()).filter(Boolean),
    metadata: {
      created_at: new Date().toISOString(),
      source: event.headers?.['user-agent'] || 'unknown',
      method: event.method,
      path: event.path,
    },
  };

  // Simulate some computation — hash-like string ops
  let checksum = 0;
  for (const ch of JSON.stringify(user)) checksum = ((checksum << 5) - checksum + ch.charCodeAt(0)) | 0;
  user.checksum = Math.abs(checksum).toString(16);

  return {
    statusCode: 201,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user, ok: true }),
  };
};
