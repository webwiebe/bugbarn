import { captureException, flush, init } from "../../sdks/typescript/src/index.ts";

const apiKey = process.env.BUGBARN_API_KEY ?? "bb_live_example";
const endpoint = process.env.BUGBARN_ENDPOINT ?? "http://127.0.0.1:8080/api/v1/events";
const mode = process.env.BUGBARN_SAMPLE_MODE ?? "manual";

init({
  apiKey,
  endpoint,
});

async function run(): Promise<void> {
  if (mode === "uncaught") {
    setTimeout(() => {
      throw new Error("BugBarn TypeScript sample uncaught exception");
    }, 0);
    return;
  }

  await captureException(new Error("BugBarn TypeScript sample manual error"), {
    attributes: {
      "service.name": "bugbarn-ts-sample",
      "runtime.name": "node",
    },
    tags: {
      sample: "typescript",
    },
    extra: {
      mode: "manual",
    },
  });
  await flush();
}

void run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});

