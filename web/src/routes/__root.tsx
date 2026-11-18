import { createRootRoute, Outlet } from "@tanstack/react-router";

export const Route = createRootRoute({
  component: RootLayout,
});

/**
 * Renders the frame around every page.
 *
 * @returns The layout with the current route inside it.
 */
function RootLayout() {
  return (
    <main className="mx-auto max-w-6xl px-6 py-8">
      <Outlet />
    </main>
  );
}
