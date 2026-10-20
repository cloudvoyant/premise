import { createFileRoute } from "@tanstack/react-router";
import { greeting } from "../lib/greeting";

export const Route = createFileRoute("/")({
  component: Home,
});

function Home() {
  return (
    <main>
      <h1>{greeting("TanStack Start")}</h1>
      <p>This page is rendered on the server and hydrated in the browser.</p>
    </main>
  );
}
