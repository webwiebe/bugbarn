import { readFile } from "node:fs/promises";

const endpoint = process.env.BUGBARN_ENDPOINT ?? "http://127.0.0.1:8080/api/v1/events";
const apiKey = process.env.BUGBARN_API_KEY ?? "bb_live_example";
const count = Number(process.env.BUGBARN_COUNT ?? "1000");
const concurrency = Number(process.env.BUGBARN_CONCURRENCY ?? "25");

const fixtureUrl = new URL("../specs/001-personal-error-tracker/fixtures/canonical/otel-event.json", import.meta.url);
const baseEvent = JSON.parse(await readFile(fixtureUrl, "utf8"));

async function send(index) {
  const payload = {
    ...baseEvent,
    timestamp: new Date(Date.now() + index).toISOString(),
    attributes: {
      ...baseEvent.attributes,
      "load.batch_index": index,
    },
  };

  const response = await fetch(endpoint, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-bugbarn-api-key": apiKey,
    },
    body: JSON.stringify(payload),
  });

  if (response.status !== 202) {
    throw new Error(`unexpected response ${response.status} for event ${index}`);
  }
}

for (let start = 0; start < count; start += concurrency) {
  const batch = [];
  for (let index = start; index < Math.min(start + concurrency, count); index += 1) {
    batch.push(send(index));
  }
  await Promise.all(batch);
}

console.log(`sent ${count} events to ${endpoint}`);
