/**
 * Pino transport for BugBarn log sink.
 *
 * Usage with Pino v8/v9:
 *
 *   import pino from 'pino'
 *   import { createBugBarnDestination } from 'bugbarn'
 *
 *   const logger = pino(
 *     createBugBarnDestination({
 *       endpoint: process.env.BUGBARN_ENDPOINT + '/api/v1/logs',
 *       apiKey: process.env.BUGBARN_INGEST_KEY,
 *       project: process.env.BUGBARN_PROJECT,   // optional with a project-scoped key
 *       level: 'warn',                          // optional: drop anything below warn
 *     })
 *   )
 *
 * With multiple destinations (stdout + BugBarn):
 *
 *   const logger = pino(pino.multistream([
 *     { stream: pino.destination(1) },   // stdout
 *     { stream: createBugBarnDestination({ endpoint, apiKey }), level: 'warn' },
 *   ]))
 */

import { Writable } from 'node:stream'

/** Pino's numeric level values, used to resolve the `level` option. */
const LEVELS: Record<string, number> = {
  trace: 10,
  debug: 20,
  info: 30,
  warn: 40,
  error: 50,
  fatal: 60,
}

export interface BugBarnTransportOptions {
  endpoint: string
  apiKey: string
  /**
   * Project slug to ingest into, sent as X-BugBarn-Project. Optional when the
   * API key is project-scoped — the server resolves the project from the key.
   * Required for a global (non project-scoped) key.
   */
  project?: string
  flushIntervalMs?: number
  batchSize?: number
  /**
   * Minimum level to send, by name (trace|debug|info|warn|error|fatal).
   * Entries below it are dropped before batching. Unset sends everything.
   */
  level?: string
}

export function createBugBarnDestination(opts: BugBarnTransportOptions): Writable {
  const { endpoint, apiKey, project, flushIntervalMs = 1000, batchSize = 50, level } = opts
  const minLevel = level ? LEVELS[level.toLowerCase()] : undefined
  if (level && minLevel === undefined) {
    throw new Error(`bugbarn: unknown level ${JSON.stringify(level)} (expected one of ${Object.keys(LEVELS).join(', ')})`)
  }
  const batch: object[] = []
  let timer: ReturnType<typeof setTimeout> | null = null

  async function flush(): Promise<void> {
    if (!batch.length) return
    const toSend = batch.splice(0)
    try {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        'X-BugBarn-Api-Key': apiKey,
      }
      if (project) headers['X-BugBarn-Project'] = project
      await globalThis.fetch(endpoint, {
        method: 'POST',
        headers,
        body: JSON.stringify({ logs: toSend }),
      })
    } catch {
      // network errors silently dropped — never crash the app
    }
  }

  function scheduleFlush(): void {
    if (!timer) {
      timer = setTimeout(() => {
        timer = null
        void flush()
      }, flushIntervalMs)
    }
  }

  return new Writable({
    write(chunk: Buffer, _encoding: BufferEncoding, callback: (err?: Error | null) => void): void {
      try {
        const line = chunk.toString().trim()
        if (line) {
          const parsed = JSON.parse(line) as { level?: number }
          if (minLevel !== undefined && typeof parsed.level === 'number' && parsed.level < minLevel) {
            callback()
            return
          }
          batch.push(parsed)
          if (batch.length >= batchSize) {
            if (timer) { clearTimeout(timer); timer = null }
            void flush()
          } else {
            scheduleFlush()
          }
        }
      } catch {
        // ignore JSON parse errors
      }
      callback()
    },
    final(callback: (err?: Error | null) => void): void {
      if (timer) { clearTimeout(timer); timer = null }
      flush().then(() => callback()).catch(() => callback())
    },
  })
}
