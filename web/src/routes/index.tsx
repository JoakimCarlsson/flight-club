import { createRoute } from "@tanstack/react-router";
import { useState } from "react";
import { ChatPanel } from "@/components/chat-panel";
import { Workspace } from "@/components/workspace";
import { useChat } from "@/hooks/use-chat";
import { cn } from "@/lib/utils";
import { Route as rootRoute } from "./__root";

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: ChatPage,
});

/**
 * Renders the assistant full screen. Before the first message the chat
 * stands alone in the middle; after it, the chat moves left and the
 * workspace showing the searches takes the rest. On narrow screens one of
 * the two is shown at a time.
 *
 * @returns The chat page.
 */
function ChatPage() {
  const chat = useChat();
  const [focus, setFocus] = useState<string | undefined>();
  const [view, setView] = useState<"chat" | "flights">("chat");
  const started = chat.messages.length > 0;

  if (!started) {
    return (
      <div className="flex h-dvh justify-center bg-surface">
        <div className="w-full max-w-xl">
          <ChatPanel
            messages={chat.messages}
            runs={chat.runs}
            onSelect={chat.select}
            onFocusOffer={() => {}}
            busy={chat.busy}
            onSend={chat.send}
            onStop={chat.stop}
          />
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-dvh flex-col bg-surface lg:grid lg:grid-cols-[420px_1fr]">
      <div className="flex border-b lg:hidden">
        {(["chat", "flights"] as const).map((v) => (
          <button
            key={v}
            type="button"
            onClick={() => setView(v)}
            className={cn(
              "flex-1 py-2.5 font-medium text-sm capitalize",
              view === v ? "border-brand border-b-2" : "text-muted-foreground",
            )}
          >
            {v}
          </button>
        ))}
      </div>
      <aside
        className={cn(
          "min-h-0 flex-1 overflow-hidden lg:block lg:h-dvh lg:border-r",
          view === "chat" ? "block" : "hidden",
        )}
      >
        <ChatPanel
          messages={chat.messages}
          runs={chat.runs}
          selectedId={chat.selected?.id}
          onSelect={(id) => {
            chat.select(id);
            setView("flights");
          }}
          onFocusOffer={(runId, key) => {
            chat.select(runId);
            setFocus(key);
            setView("flights");
          }}
          busy={chat.busy}
          onSend={chat.send}
          onStop={chat.stop}
        />
      </aside>
      <main
        className={cn(
          "min-h-0 flex-1 overflow-hidden bg-background lg:block lg:h-dvh",
          view === "flights" ? "block" : "hidden",
        )}
      >
        <Workspace run={chat.selected} focus={focus} />
      </main>
    </div>
  );
}
