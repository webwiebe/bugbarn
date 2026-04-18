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

export interface BugBarnTransportOptions {
  endpoint: string
  apiKey: string
  flushIntervalMs?: number
  batchSize?: number
  level?: string
}

export function createBugBarnDestination(opts: BugBarnTransportOptions): Writable {
  const { endpoint, apiKey, flushIntervalMs = 1000, batchSize = 50 } = opts
  const batch: object[] = []
  let timer: ReturnType<typeof setTimeout> | null = null

  async function flush(): Promise<void> {
    if (!batch.length) return
    const toSend = batch.splice(0)
    try {
      await globalThis.fetch(endpoint, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-BugBarn-Api-Key': apiKey,
        },
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
          const parsed = JSON.parse(line) as object
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
