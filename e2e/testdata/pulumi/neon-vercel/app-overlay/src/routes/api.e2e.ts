import { createFileRoute } from "@tanstack/react-router";
import { neon } from "@neondatabase/serverless";
import { release } from "../release";

/** Only this server route reads DATABASE_URL; it never enters a browser bundle. */
export const Route = createFileRoute("/api/e2e")({
  server: {
    handlers: {
      GET: async () => {
        if (!process.env.APP_MARKER)
          return new Response("Application secret unavailable", {
            status: 503,
          });
        const sql = neon(process.env.DATABASE_URL!);
        const rows = await sql`SELECT id, value FROM premise_e2e ORDER BY id`;
        return Response.json({ release, rows });
      },
      POST: async ({ request }) => {
        const body = (await request.json()) as { id?: string; value?: string };
        if (!body.id || !body.value) return new Response("id and value required", { status: 400 });
        if (!process.env.APP_MARKER)
          return new Response("Application secret unavailable", {
            status: 503,
          });
        const sql = neon(process.env.DATABASE_URL!);
        await sql`INSERT INTO premise_e2e (id, value) VALUES (${body.id}, ${body.value}) ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value`;
        return Response.json({ release, written: body.id });
      },
    },
  },
});
