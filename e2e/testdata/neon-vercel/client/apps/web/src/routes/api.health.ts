import { artifactRelease } from "../lib/release";
import { createFileRoute } from "@tanstack/react-router";
import postgres from "postgres";

async function databaseMessage(connection: string) {
  const sql = postgres(connection, { ssl: "require", max: 1 });
  try {
    const [table] = await sql`select to_regclass('public.messages') as name`;
    if (!table.name) return "unmigrated";
    const [row] = await sql`select message from messages where id = 1`;
    return String(row.message);
  } finally {
    await sql.end();
  }
}

export const Route = createFileRoute("/api/health")({
  server: {
    handlers: {
      GET: async () => {
        const api = await databaseMessage(process.env.API_DATABASE_URL!);
        const worker = await databaseMessage(process.env.WORKER_DATABASE_URL!);
        return Response.json({
          release: process.env.PREMISE_RELEASE,
          artifact: artifactRelease,
          api,
          worker,
        });
      },
    },
  },
});
