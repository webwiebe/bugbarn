# Examples

These are validation-only examples for local BugBarn development.

## TypeScript sample

```bash
BUGBARN_API_KEY=bb_live_example \
BUGBARN_ENDPOINT=http://127.0.0.1:8080/api/v1/events \
node --experimental-strip-types examples/typescript/sample.ts
```

Set `BUGBARN_SAMPLE_MODE=uncaught` to trigger the SDK's default uncaught handler path.

## Python sample

```bash
PYTHONPATH=sdks/python/src \
BUGBARN_API_KEY=bb_live_example \
BUGBARN_ENDPOINT=http://127.0.0.1:8080/api/v1/events \
python3 examples/python/sample.py
```

Set `BUGBARN_SAMPLE_MODE=uncaught` to trigger the `sys.excepthook` path.

## Load generator

```bash
BUGBARN_API_KEY=bb_live_example \
BUGBARN_ENDPOINT=http://127.0.0.1:8080/api/v1/events \
BUGBARN_COUNT=1000 \
BUGBARN_CONCURRENCY=25 \
node examples/load-fixture.mjs
```

The generator uses only built-in Node modules and the canonical fixture in `specs/001-personal-error-tracker/fixtures/`.

