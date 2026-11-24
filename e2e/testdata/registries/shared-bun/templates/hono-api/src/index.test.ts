import { expect, test } from "bun:test";
import { appName } from "./index";

test("uses generated name", () => {
  expect(appName).toBe("hono-api");
});
