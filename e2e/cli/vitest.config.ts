import { defineConfig } from "vitest/config";
import { suiteConfig } from "../../libs/testutils/config.js";

export default defineConfig(suiteConfig(import.meta.dirname));
