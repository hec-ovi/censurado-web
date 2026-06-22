// Vitest setup: run the MSW interception lifecycle around every test. Any HTTP
// request the refiner makes that no test handler covers fails loudly, so a
// missing or wrong shard/manifest URL is caught instead of silently hanging.
import { afterAll, afterEach, beforeAll } from "vitest";
import { server } from "./server.js";

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
