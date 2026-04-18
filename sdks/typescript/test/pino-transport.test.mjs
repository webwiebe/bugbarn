import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createBugBarnDestination } from '../dist/esm/pino-transport.js'

test('batches and flushes on interval', async () => {
  const sent = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(JSON.parse(opts.body))
    return { ok: true, status: 200 }
  }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', flushIntervalMs: 50 })
  dest.write(JSON.stringify({ level: 30, msg: 'hello' }) + '\n')
  dest.write(JSON.stringify({ level: 50, msg: 'error' }) + '\n')
  dest.write(JSON.stringify({ level: 30, msg: 'third' }) + '\n')

  await new Promise(r => setTimeout(r, 150))

  assert.equal(sent.length, 1)
  assert.equal(sent[0].logs.length, 3)
})

test('flushes immediately at batchSize', async () => {
  const sent = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(JSON.parse(opts.body))
    return { ok: true, status: 200 }
  }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', flushIntervalMs: 5000, batchSize: 5 })
  for (let i = 0; i < 5; i++) {
    dest.write(JSON.stringify({ level: 30, msg: `msg-${i}` }) + '\n')
  }

  // Give the microtask queue a chance to process the async flush
  await new Promise(r => setTimeout(r, 50))

  assert.equal(sent.length, 1)
  assert.equal(sent[0].logs.length, 5)
})

test('ignores JSON parse errors and sends only valid lines', async () => {
  const sent = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(JSON.parse(opts.body))
    return { ok: true, status: 200 }
  }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', flushIntervalMs: 50 })
  dest.write('not-valid-json\n')
  dest.write(JSON.stringify({ level: 30, msg: 'valid' }) + '\n')

  await new Promise(r => setTimeout(r, 150))

  assert.equal(sent.length, 1)
  assert.equal(sent[0].logs.length, 1)
  assert.equal(sent[0].logs[0].msg, 'valid')
})

test('swallows fetch errors without propagating', async () => {
  globalThis.fetch = async () => { throw new Error('network failure') }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', flushIntervalMs: 50 })
  dest.write(JSON.stringify({ level: 30, msg: 'hello' }) + '\n')

  // Wait for interval flush — should not throw
  await new Promise(r => setTimeout(r, 150))
  // If we reach here, no exception was propagated
  assert.ok(true)
})

test('flushes on stream end', async () => {
  const sent = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(JSON.parse(opts.body))
    return { ok: true, status: 200 }
  }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', flushIntervalMs: 5000 })
  dest.write(JSON.stringify({ level: 30, msg: 'before-end' }) + '\n')

  await new Promise((resolve, reject) => {
    dest.end(() => resolve())
    dest.on('error', reject)
  })

  assert.equal(sent.length, 1)
  assert.equal(sent[0].logs[0].msg, 'before-end')
})
