import { Client } from "pg";
import { drizzle } from "drizzle-orm/node-postgres";
import { migrate } from "drizzle-orm/node-postgres/migrator";

if (!process.env.APP_MARKER) throw new Error("Application fnox secret profile was not injected");
const client = new Client({ connectionString: process.env.DATABASE_URL });
await client.connect();
try {
  await client.query("SELECT pg_advisory_lock(hashtext('premise-e2e-migration'))");
  await migrate(drizzle(client), { migrationsFolder: "migrations" });
} finally {
  await client.query("SELECT pg_advisory_unlock(hashtext('premise-e2e-migration'))");
  await client.end();
}
