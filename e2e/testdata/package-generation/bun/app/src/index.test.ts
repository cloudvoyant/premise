import { expect, test } from "bun:test";
import { message } from "LIBRARY_NAME";
test("uses local sibling", () => expect(message()).toBe("hello"));
