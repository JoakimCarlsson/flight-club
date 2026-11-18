import { createRoute } from "@tanstack/react-router";
import { Route as rootRoute } from "./__root";

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: HomePage,
});

/**
 * Renders the start page.
 *
 * @returns The start page.
 */
function HomePage() {
  return <h1 className="font-semibold text-2xl">Flight Club</h1>;
}
