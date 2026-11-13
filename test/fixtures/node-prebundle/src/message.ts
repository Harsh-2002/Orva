import { posix } from 'node:path';

export function message(name: string): string {
  return `Hello ${posix.basename(name)}`;
}
