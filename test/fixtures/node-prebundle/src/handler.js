const { message } = require('./message.js');
const { kv } = require('orva');

exports.handler = async event => {
  const { name } = JSON.parse(event.body || '{}');
  await kv.put('prebundle:last', name);
  const saved = await kv.get('prebundle:last', null);
  return {
    statusCode: 201,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ revision: 'bundle-A', language: 'js', message: message(name), saved }),
  };
};
