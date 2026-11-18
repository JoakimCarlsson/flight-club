import { ArrowUp, Loader2, Plane, Square } from "lucide-react";
import {
  type FormEvent,
  type KeyboardEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import Markdown from "react-markdown";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { Message, Run } from "@/hooks/use-chat";
import { cn } from "@/lib/utils";

/** Questions offered on an empty chat. */
const EXAMPLES = [
  "Seattle to New York on 15 December, two people",
  "Business class from Seattle to London next week",
  "SEA to NYC 15–17 December, back 20 December",
];

/**
 * Renders the conversation and the box to write the next message in.
 *
 * @param props.messages - The conversation.
 * @param props.runs - Every search run, by id.
 * @param props.selectedId - The search shown in the workspace.
 * @param props.onSelect - Shows a search in the workspace.
 * @param props.busy - Set while the assistant is replying.
 * @param props.onSend - Sends a message.
 * @param props.onStop - Stops the reply in flight.
 * @returns The chat panel.
 */
export function ChatPanel({
  messages,
  runs,
  selectedId,
  onSelect,
  busy,
  onSend,
  onStop,
}: {
  messages: Message[];
  runs: Record<string, Run>;
  selectedId?: string;
  onSelect: (id: string) => void;
  busy: boolean;
  onSend: (text: string) => void;
  onStop: () => void;
}) {
  const [draft, setDraft] = useState("");
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  /**
   * Sends the draft and clears the box.
   *
   * @param e - The submit event, when sent by the form.
   */
  function submit(e?: FormEvent) {
    e?.preventDefault();
    if (!draft.trim() || busy) return;
    onSend(draft);
    setDraft("");
  }

  /**
   * Sends on Enter; Shift+Enter writes a new line.
   *
   * @param e - The key event.
   */
  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-2 border-b px-4 py-3 font-semibold">
        <Plane className="size-4" />
        Flight Club
      </header>

      <div className="flex-1 overflow-y-auto px-4 py-4">
        {messages.length === 0 ? (
          <div className="flex h-full flex-col justify-center gap-3">
            <p className="text-muted-foreground text-sm">
              Ask for award flights in plain words.
            </p>
            {EXAMPLES.map((q) => (
              <button
                key={q}
                type="button"
                onClick={() => onSend(q)}
                className="rounded-lg border px-3 py-2 text-left text-sm hover:bg-accent"
              >
                {q}
              </button>
            ))}
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            {messages.map((m) => (
              <MessageView
                key={m.id}
                message={m}
                runs={runs}
                selectedId={selectedId}
                onSelect={onSelect}
              />
            ))}
            <div ref={bottom} />
          </div>
        )}
      </div>

      <form onSubmit={submit} className="border-t p-3">
        <div className="relative">
          <Textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="Where do you want to fly?"
            rows={2}
            className="resize-none pr-12"
          />
          <Button
            type={busy ? "button" : "submit"}
            size="icon"
            onClick={busy ? onStop : undefined}
            disabled={!busy && !draft.trim()}
            className="absolute right-2 bottom-2 size-8"
          >
            {busy ? (
              <Square className="size-3" />
            ) : (
              <ArrowUp className="size-4" />
            )}
          </Button>
        </div>
      </form>
    </div>
  );
}

/**
 * Renders one message, with a chip for every search run while writing it.
 *
 * @param props.message - The message.
 * @param props.runs - Every search run, by id.
 * @param props.selectedId - The search shown in the workspace.
 * @param props.onSelect - Shows a search in the workspace.
 * @returns The message.
 */
function MessageView({
  message,
  runs,
  selectedId,
  onSelect,
}: {
  message: Message;
  runs: Record<string, Run>;
  selectedId?: string;
  onSelect: (id: string) => void;
}) {
  if (message.role === "user") {
    return (
      <div className="ml-8 self-end rounded-2xl bg-primary px-3 py-2 text-primary-foreground text-sm">
        {message.text}
      </div>
    );
  }
  return (
    <div className="mr-8 flex flex-col gap-2">
      {message.runs.map((id) => (
        <RunChip
          key={id}
          run={runs[id]}
          active={id === selectedId}
          onClick={() => onSelect(id)}
        />
      ))}
      {message.text && (
        <div className="prose prose-sm text-sm leading-relaxed [&_li]:my-0.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-1.5 [&_ul]:list-disc [&_ul]:pl-5">
          <Markdown>{message.text}</Markdown>
        </div>
      )}
      {message.pending && !message.text && (
        <Loader2 className="size-4 animate-spin text-muted-foreground" />
      )}
      {message.error && (
        <p className="text-destructive text-sm">{message.error}</p>
      )}
    </div>
  );
}

/**
 * Renders a search the assistant ran as a chip that opens it in the
 * workspace.
 *
 * @param props.run - The search.
 * @param props.active - Set when it is the one shown.
 * @param props.onClick - Shows it.
 * @returns The chip.
 */
function RunChip({
  run,
  active,
  onClick,
}: {
  run?: Run;
  active: boolean;
  onClick: () => void;
}) {
  const s = run?.search;
  const running = run && !run.results && !run.failed;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex items-center gap-2 self-start rounded-md border border-dashed px-2 py-1 text-muted-foreground text-xs hover:bg-accent",
        active && "border-solid bg-accent text-foreground",
      )}
    >
      {running ? (
        <Loader2 className="size-3 animate-spin" />
      ) : (
        <Plane className="size-3" />
      )}
      {s
        ? `${s.origin} → ${s.destination} · ${s.departFrom}${s.departTo !== s.departFrom ? ` – ${s.departTo}` : ""}`
        : "Searching…"}
      {run?.results && ` · ${run.results.offers.length} offers`}
    </button>
  );
}
