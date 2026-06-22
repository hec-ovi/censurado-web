// Shared MSW server for the refiner tests. Handlers are registered per test
// with server.use(...); this module just owns the singleton.
import { setupServer } from "msw/node";

export const server = setupServer();
