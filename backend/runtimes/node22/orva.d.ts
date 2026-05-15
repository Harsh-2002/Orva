// Orva Node.js SDK — TypeScript declarations.
// Ships beside orva.js in the runtime bundle so TS handlers get full
// IntelliSense without an external @types package.

export const SDK_VERSION: string

export class OrvaError extends Error {
  constructor(message: string, status?: number)
  status: number
}

export class OrvaUnavailableError extends OrvaError {}

export class OrvaCASMismatch extends OrvaError {
  currentValue: unknown
}

export interface KVListEntry<T = unknown> {
  key: string
  value: T
  expires_at?: string
}

export interface KVListResult<T = unknown> {
  keys: KVListEntry<T>[]
  nextCursor: string
}

export interface KVPutEntry<T = unknown> {
  key: string
  value: T
  ttlSeconds?: number
}

export interface KVOptions {
  ttlSeconds?: number
}

export interface KV {
  get<T = unknown>(key: string, defaultValue?: T | null): Promise<T | null>
  put<T>(key: string, value: T, opts?: KVOptions): Promise<void>
  delete(key: string): Promise<void>
  list<T = unknown>(opts?: {
    prefix?: string
    limit?: number
    cursor?: string
  }): Promise<KVListResult<T>>
  getMany<T = unknown>(keys: string[]): Promise<Record<string, T | null>>
  putMany<T>(entries: KVPutEntry<T>[]): Promise<void>
  deleteMany(keys: string[]): Promise<number>
  incr(key: string, delta?: number, opts?: KVOptions): Promise<number>
  cas<T>(
    key: string,
    expected: T | null,
    next: T,
    opts?: KVOptions
  ): Promise<true>
}

export const kv: KV

export interface InvokeEnvelope<T = unknown> {
  statusCode: number
  headers: Record<string, string>
  body: T
}

export interface InvokeOptions {
  timeoutMs?: number
}

export function invoke<Req = unknown, Res = unknown>(
  name: string,
  payload?: Req,
  opts?: InvokeOptions
): Promise<InvokeEnvelope<Res>>

export function invokeStream(
  name: string,
  payload?: unknown,
  opts?: InvokeOptions
): AsyncIterable<Uint8Array>

export interface EnqueueOptions {
  maxAttempts?: number
  scheduledAt?: string
  idempotencyKey?: string
  idempotencyWindowSeconds?: number
}

export interface EnqueueResult {
  id: string
  replayed: boolean
}

export const jobs: {
  enqueue(
    name: string,
    payload?: unknown,
    opts?: EnqueueOptions
  ): Promise<EnqueueResult>
}

export interface CronUpsertOptions {
  payload?: unknown
  timezone?: string
  enabled?: boolean
}

export interface CronUpsertResult {
  id: string
  function_id: string
  name: string
  schedule: string
  timezone: string
  enabled: boolean
}

export const crons: {
  upsert(
    name: string,
    schedule: string,
    opts?: CronUpsertOptions
  ): Promise<CronUpsertResult>
}

export const trace: {
  span<T>(
    name: string,
    fn: () => Promise<T> | T,
    attrs?: Record<string, unknown>
  ): Promise<T>
}

export type LogFields = Record<string, unknown>

export const log: {
  debug(msg: string, fields?: LogFields): void
  info(msg: string, fields?: LogFields): void
  warn(msg: string, fields?: LogFields): void
  error(msg: string, fields?: LogFields): void
}

export const secrets: { get(name: string): string | undefined }

export type WebhookSource = 'github' | 'stripe' | 'slack' | 'hmac' | 'unknown'

export interface ParsedWebhook<T = unknown> {
  verified: boolean
  source: WebhookSource
  eventType?: string
  webhookId?: string
  payload: T
  headers: Record<string, string>
}

export const webhook: {
  parse<T = unknown>(event: {
    headers: Record<string, string>
    body: string
  }): ParsedWebhook<T>
}

export interface OrvaContext {
  functionId: string
  executionId: string
  traceId: string
  spanId: string
  callDepth: number
  timeoutMs: number
  memoryMb: number
  sdkVersion: string
}

export const context: OrvaContext

export interface TestImpl {
  request?: (
    method: string,
    path: string,
    opts?: {
      body?: unknown
      headers?: Record<string, string>
      timeoutMs?: number
    }
  ) => Promise<{ status: number; body: string; headers?: any }>
}

export function __test_mode__(impl: TestImpl | null): void
