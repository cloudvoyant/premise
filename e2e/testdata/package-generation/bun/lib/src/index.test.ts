import { expect, test } from "bun:test";
import { message } from "./index";
test("returns message", () => expect(message()).toBe("hello"));
