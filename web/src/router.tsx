import { createRouter } from "@tanstack/react-router";
import { Route as rootRoute } from "./routes/__root";
import { Route as indexRoute } from "./routes/index";

/** The route tree. */
const routeTree = rootRoute.addChildren([indexRoute]);

/** The router, registered with TanStack Router for typed navigation. */
export const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
