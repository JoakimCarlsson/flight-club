import { useCallback, useRef, useState } from "react";
import {
  type AwardResult,
  type ChatEvent,
  type SearchView,
  type Step,
  streamChat,
} from "@/lib/chat";

/** One message of the conversation, as the chat shows it. */
export interface Message {
  id: string;
  role: "user" | "assistant";
  text: string;
  /** Ids of the searches the assistant ran while writing it. */
  runs: string[];
  /** Set while the assistant is still writing it. */
  pending?: boolean;
  /** Set when the turn failed. */
  error?: string;
}

/** One search the assistant ran, as the workspace shows it. */
export interface Run {
  id: string;
  search?: SearchView;
  /** Program-days keyed by program, direction and date. */
  steps: Record<string, Step>;
  results?: AwardResult;
  failed?: boolean;
}

/**
 * Holds the conversation and the searches run during it, and sends new
 * messages to the assistant.
 *
 * @returns The messages, the runs, the selected run, whether a turn is in
 *   flight, and functions to send a message and select a run.
 */
export function useChat() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [runs, setRuns] = useState<Record<string, Run>>({});
  const [selected, setSelected] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const abort = useRef<AbortController | null>(null);

  const send = useCallback(
    async (text: string) => {
      const trimmed = text.trim();
      if (!trimmed || busy) return;

      const user: Message = {
        id: crypto.randomUUID(),
        role: "user",
        text: trimmed,
        runs: [],
      };
      const replyId = crypto.randomUUID();
      const history = [...messages, user]
        .filter((m) => !m.error && m.text)
        .map((m) => ({ role: m.role, text: m.text }));
      setMessages((prev) => [
        ...prev,
        user,
        { id: replyId, role: "assistant", text: "", runs: [], pending: true },
      ]);
      setBusy(true);

      const updateReply = (change: (m: Message) => Message) =>
        setMessages((prev) =>
          prev.map((m) => (m.id === replyId ? change(m) : m)),
        );
      const updateRun = (id: string, change: (r: Run) => Run) =>
        setRuns((prev) => ({
          ...prev,
          [id]: change(prev[id] ?? { id, steps: {} }),
        }));

      /**
       * Applies one event of the turn to the conversation and the runs.
       *
       * @param ev - The event.
       */
      const onEvent = (ev: ChatEvent) => {
        switch (ev.event) {
          case "text":
            updateReply((m) => ({ ...m, text: m.text + (ev.text ?? "") }));
            break;
          case "search":
            if (!ev.callId) break;
            updateRun(ev.callId, (r) => ({ ...r, search: ev.search }));
            updateReply((m) => ({ ...m, runs: [...m.runs, ev.callId!] }));
            setSelected(ev.callId);
            break;
          case "step":
            if (!ev.callId || !ev.step) break;
            updateRun(ev.callId, (r) => ({
              ...r,
              steps: { ...r.steps, [stepKey(ev.step!)]: ev.step! },
            }));
            break;
          case "results":
            if (!ev.callId) break;
            updateRun(ev.callId, (r) => ({ ...r, results: ev.results }));
            break;
          case "toolResult":
            if (ev.callId && ev.failed) {
              updateRun(ev.callId, (r) => ({ ...r, failed: true }));
            }
            break;
          case "done":
            updateReply((m) => ({
              ...m,
              text: ev.text || m.text,
              pending: false,
            }));
            break;
          case "error":
            updateReply((m) => ({ ...m, pending: false, error: ev.error }));
            break;
        }
      };

      abort.current = new AbortController();
      try {
        await streamChat(history, onEvent, abort.current.signal);
      } catch (err) {
        if (!abort.current.signal.aborted) {
          updateReply((m) => ({
            ...m,
            error: err instanceof Error ? err.message : String(err),
          }));
        }
      } finally {
        updateReply((m) => ({ ...m, pending: false }));
        setBusy(false);
      }
    },
    [busy, messages],
  );

  const stop = useCallback(() => abort.current?.abort(), []);

  return {
    messages,
    runs,
    selected: selected ? runs[selected] : undefined,
    select: setSelected,
    busy,
    send,
    stop,
  };
}

/**
 * Keys a step by what it searched, so its finish replaces its start.
 *
 * @param step - The step.
 * @returns The key.
 */
export function stepKey(step: Step): string {
  return `${step.program}|${step.direction}|${step.date}`;
}
