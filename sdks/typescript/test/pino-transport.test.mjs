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

test('sends X-BugBarn-Project when project is set', async () => {
  const sent = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(opts.headers)
    return { ok: true, status: 200 }
  }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', project: 'svc', flushIntervalMs: 50 })
  dest.write(JSON.stringify({ level: 30, msg: 'hello' }) + '\n')
  await new Promise(r => setTimeout(r, 150))

  assert.equal(sent.length, 1)
  assert.equal(sent[0]['X-BugBarn-Project'], 'svc')
})

test('omits X-BugBarn-Project when project is unset', async () => {
  const sent = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(opts.headers)
    return { ok: true, status: 200 }
  }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', flushIntervalMs: 50 })
  dest.write(JSON.stringify({ level: 30, msg: 'hello' }) + '\n')
  await new Promise(r => setTimeout(r, 150))

  assert.equal(sent.length, 1)
  assert.ok(!('X-BugBarn-Project' in sent[0]))
})

test('level filters out entries below the threshold', async () => {
  const sent = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(JSON.parse(opts.body))
    return { ok: true, status: 200 }
  }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', level: 'warn', flushIntervalMs: 50 })
  dest.write(JSON.stringify({ level: 30, msg: 'info' }) + '\n')
  dest.write(JSON.stringify({ level: 40, msg: 'warn' }) + '\n')
  dest.write(JSON.stringify({ level: 50, msg: 'error' }) + '\n')
  await new Promise(r => setTimeout(r, 150))

  assert.equal(sent.length, 1)
  assert.deepEqual(sent[0].logs.map(l => l.msg), ['warn', 'error'])
})

test('level below threshold never triggers a request', async () => {
  let calls = 0
  globalThis.fetch = async () => { calls++; return { ok: true, status: 200 } }

  const dest = createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', level: 'error', flushIntervalMs: 50 })
  dest.write(JSON.stringify({ level: 30, msg: 'info' }) + '\n')
  await new Promise(r => setTimeout(r, 150))

  assert.equal(calls, 0)
})

test('rejects an unknown level instead of silently ignoring it', () => {
  assert.throws(
    () => createBugBarnDestination({ endpoint: 'http://test/api/v1/logs', apiKey: 'k', level: 'verbose' }),
    /unknown level/,
  )
})
