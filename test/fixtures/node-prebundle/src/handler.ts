import { message } from './message';
const { kv } = require('orva');

interface Payload { name: string }

export async function handler(event: { body: string }) {
  const { name }: Payload = JSON.parse(event.body || '{}');
  await kv.put('prebundle:last', name);
  const saved = await kv.get('prebundle:last', null);
  return {
    statusCode: 201,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ revision: 'bundle-A', language: 'ts', message: message(name), saved }),
  };
}
