import { createRoute } from "@tanstack/react-router";
import { ChatPanel } from "@/components/chat-panel";
import { Workspace } from "@/components/workspace";
import { useChat } from "@/hooks/use-chat";
import { Route as rootRoute } from "./__root";

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: ChatPage,
});

/**
 * Renders the assistant full screen: the chat on the left and, on the
 * right, the workspace showing the search it is running.
 *
 * @returns The chat page.
 */
function ChatPage() {
  const chat = useChat();
  return (
    <div className="grid h-screen grid-cols-[400px_1fr] overflow-hidden">
      <aside className="h-screen overflow-hidden border-r">
        <ChatPanel
          messages={chat.messages}
          runs={chat.runs}
          selectedId={chat.selected?.id}
          onSelect={chat.select}
          busy={chat.busy}
          onSend={chat.send}
          onStop={chat.stop}
        />
      </aside>
      <main className="h-screen overflow-hidden bg-muted/30">
        <Workspace run={chat.selected} />
      </main>
    </div>
  );
}
