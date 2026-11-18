import { ArrowUp, Loader2, PlaneTakeoff, Square } from "lucide-react";
import { motion } from "motion/react";
import {
  type FormEvent,
  type KeyboardEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import Markdown from "react-markdown";
import { AirlineLogo } from "@/components/airline-logo";
import type { Message, Run } from "@/hooks/use-chat";
import type { Offer } from "@/lib/chat";
import {
  airlineName,
  dayOffset,
  describeStops,
  formatDate,
  formatMoney,
  formatPoints,
  formatRange,
  isDailyPrice,
  localDate,
  localTime,
  logoCarrier,
  offerKey,
  rank,
} from "@/lib/offers";
import { cn } from "@/lib/utils";

/** Questions offered on an empty chat. */
const EXAMPLES = [
  "Seattle to New York on 15 December, two people",
  "Business class Seattle to London, 10–14 March",
  "SEA to NYC 15–17 Dec, back on the 20th",
];

/**
 * Renders the conversation and the box to write the next message in.
 *
 * @param props.messages - The conversation.
 * @param props.runs - Every search run, by id.
 * @param props.selectedId - The search shown in the workspace.
 * @param props.onSelect - Shows a search in the workspace.
 * @param props.onFocusOffer - Shows a search and opens one of its offers.
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
  onFocusOffer,
  busy,
  onSend,
  onStop,
}: {
  messages: Message[];
  runs: Record<string, Run>;
  selectedId?: string;
  onSelect: (id: string) => void;
  onFocusOffer: (runId: string, key: string) => void;
  busy: boolean;
  onSend: (text: string) => void;
  onStop: () => void;
}) {
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages]);

  if (messages.length === 0) {
    return (
      <div className="flex h-full flex-col justify-center gap-8 px-6">
        <div className="flex flex-col gap-3">
          <span className="flex size-10 items-center justify-center rounded-xl bg-brand text-brand-foreground">
            <PlaneTakeoff className="size-5" />
          </span>
          <h1 className="font-semibold text-2xl tracking-tight">
            Where do you want to fly with your points?
          </h1>
          <p className="text-muted-foreground text-sm">
            Say where, when and how many. I search the loyalty programs live and
            show every award seat I find.
          </p>
        </div>
        <Composer busy={busy} onSend={onSend} onStop={onStop} autoFocus />
        <div className="flex flex-col gap-2">
          {EXAMPLES.map((q) => (
            <button
              key={q}
              type="button"
              onClick={() => onSend(q)}
              className="rounded-lg px-3 py-2 text-left text-muted-foreground text-sm transition-colors hover:bg-muted hover:text-foreground"
            >
              {q} →
            </button>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-2 px-5 py-4 font-semibold tracking-tight">
        <span className="flex size-6 items-center justify-center rounded-md bg-brand text-brand-foreground">
          <PlaneTakeoff className="size-3.5" />
        </span>
        Flight Club
      </header>

      <div className="flex-1 overflow-y-auto px-5 pb-4">
        <div className="flex flex-col gap-5">
          {messages.map((m) => (
            <MessageView
              key={m.id}
              message={m}
              runs={runs}
              selectedId={selectedId}
              onSelect={onSelect}
              onFocusOffer={onFocusOffer}
            />
          ))}
          <div ref={bottom} />
        </div>
      </div>

      <div className="p-4 pt-0">
        <Composer busy={busy} onSend={onSend} onStop={onStop} />
      </div>
    </div>
  );
}

/**
 * Renders the box the next message is written in. Enter sends,
 * Shift+Enter starts a new line; while the assistant replies, the button
 * stops it.
 *
 * @param props.busy - Set while the assistant is replying.
 * @param props.onSend - Sends a message.
 * @param props.onStop - Stops the reply in flight.
 * @param props.autoFocus - Focuses the box on mount.
 * @returns The composer.
 */
function Composer({
  busy,
  onSend,
  onStop,
  autoFocus,
}: {
  busy: boolean;
  onSend: (text: string) => void;
  onStop: () => void;
  autoFocus?: boolean;
}) {
  const [draft, setDraft] = useState("");
  const box = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (autoFocus) box.current?.focus();
  }, [autoFocus]);

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
    <form
      onSubmit={submit}
      className="flex items-end gap-2 rounded-2xl border bg-background p-2 shadow-sm transition-shadow focus-within:shadow-md focus-within:ring-1 focus-within:ring-ring"
    >
      <textarea
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        placeholder="Stockholm to New York in March, two people…"
        rows={1}
        ref={box}
        className="field-sizing-content max-h-40 min-h-9 flex-1 resize-none bg-transparent px-2 py-1.5 text-sm outline-none placeholder:text-muted-foreground"
      />
      <button
        type={busy ? "button" : "submit"}
        onClick={busy ? onStop : undefined}
        disabled={!busy && !draft.trim()}
        aria-label={busy ? "Stop" : "Send"}
        className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-foreground text-background transition-opacity disabled:opacity-30"
      >
        {busy ? (
          <Square className="size-3 fill-current" />
        ) : (
          <ArrowUp className="size-4" />
        )}
      </button>
    </form>
  );
}

/**
 * Renders one message. An assistant message shows a card for every search
 * it ran, above its text.
 *
 * @param props.message - The message.
 * @param props.runs - Every search run, by id.
 * @param props.selectedId - The search shown in the workspace.
 * @param props.onSelect - Shows a search in the workspace.
 * @param props.onFocusOffer - Shows a search and opens one of its offers.
 * @returns The message.
 */
function MessageView({
  message,
  runs,
  selectedId,
  onSelect,
  onFocusOffer,
}: {
  message: Message;
  runs: Record<string, Run>;
  selectedId?: string;
  onSelect: (id: string) => void;
  onFocusOffer: (runId: string, key: string) => void;
}) {
  if (message.role === "user") {
    return (
      <motion.div
        initial={{ opacity: 0, y: 6 }}
        animate={{ opacity: 1, y: 0 }}
        className="ml-10 self-end rounded-2xl rounded-br-md bg-muted px-4 py-2.5 text-sm"
      >
        {message.text}
      </motion.div>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      {message.runs.map((id) => {
        const run = runs[id];
        return run ? (
          <RunCard
            key={id}
            run={run}
            active={id === selectedId}
            onOpen={() => onSelect(id)}
            onOffer={(key) => onFocusOffer(id, key)}
          />
        ) : null;
      })}
      {message.text && (
        <div className="text-sm leading-relaxed [&_li]:my-1 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-2 [&_strong]:font-semibold [&_ul]:list-disc [&_ul]:pl-5 [&>*:first-child]:mt-0">
          <Markdown>{message.text}</Markdown>
        </div>
      )}
      {message.pending && !message.text && message.runs.length === 0 && (
        <span className="flex items-center gap-2 text-muted-foreground text-sm">
          <Loader2 className="size-3.5 animate-spin" />
          Reading your request…
        </span>
      )}
      {message.error && (
        <p className="text-destructive text-sm">{message.error}</p>
      )}
    </div>
  );
}

/**
 * Renders a search the assistant ran as a card: the trip, how far it got,
 * and once offers are in, the best one, which opens in the workspace.
 *
 * @param props.run - The search.
 * @param props.active - Set when it is the one shown in the workspace.
 * @param props.onOpen - Shows it in the workspace.
 * @param props.onOffer - Shows it and opens one of its offers.
 * @returns The card.
 */
function RunCard({
  run,
  active,
  onOpen,
  onOffer,
}: {
  run: Run;
  active: boolean;
  onOpen: () => void;
  onOffer: (key: string) => void;
}) {
  const s = run.search;
  const steps = Object.values(run.steps);
  const doneSteps = steps.filter((x) => x.state !== "running").length;
  const finished = !!run.results || run.failed;
  const best = rank(
    run.offers.filter((o) => o.direction === "outbound"),
    "best",
  )[0];

  return (
    <motion.div
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      className={cn(
        "overflow-hidden rounded-xl border bg-background transition-shadow",
        active && "shadow-sm ring-1 ring-brand/40",
      )}
    >
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full items-center justify-between gap-3 px-3.5 py-2.5 text-left hover:bg-muted/50"
      >
        <span className="flex flex-col">
          <span className="font-medium text-sm">
            {s ? `${s.origin} → ${s.destination}` : "Searching…"}
          </span>
          {s && (
            <span className="text-muted-foreground text-xs">
              {formatRange(s.departFrom, s.departTo)}
              {s.returnFrom ? " · return" : ""} · {s.passengers} pax
            </span>
          )}
        </span>
        <span className="tabular flex items-center gap-1.5 text-muted-foreground text-xs">
          {!finished && <Loader2 className="size-3 animate-spin" />}
          {finished
            ? `${run.offers.length} offers`
            : steps.length
              ? `${doneSteps}/${steps.length} days`
              : "starting"}
        </span>
      </button>
      {!finished && steps.length > 0 && (
        <div className="h-0.5 bg-muted">
          <motion.div
            className="h-full bg-brand"
            animate={{ width: `${(doneSteps / steps.length) * 100}%` }}
          />
        </div>
      )}
      {best && (
        <BestOffer offer={best} onClick={() => onOffer(offerKey(best))} />
      )}
    </motion.div>
  );
}

/**
 * Renders the best offer of a search as a compact row in the chat.
 *
 * @param props.offer - The offer.
 * @param props.onClick - Opens it in the workspace.
 * @returns The row.
 */
function BestOffer({
  offer: o,
  onClick,
}: {
  offer: Offer;
  onClick: () => void;
}) {
  const plus = dayOffset(o);
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-3 border-t px-3.5 py-3 text-left hover:bg-muted/50"
    >
      <AirlineLogo code={logoCarrier(o)} className="size-8" />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="tabular font-medium text-sm">
          {isDailyPrice(o)
            ? `From ${formatDate(localDate(o.departure))}`
            : `${localTime(o.departure)} – ${localTime(o.arrival)}`}
          {plus > 0 && <sup className="ml-0.5 text-dear">+{plus}</sup>}
          <span className="ml-2 rounded-full bg-brand/15 px-1.5 py-0.5 font-medium text-[10px] text-brand">
            Best
          </span>
        </span>
        <span className="truncate text-muted-foreground text-xs">
          {formatDate(localDate(o.departure))} ·{" "}
          {o.airlines.map(airlineName).join(", ")} · {describeStops(o)} ·{" "}
          <span className="capitalize">{o.cabin}</span>
        </span>
      </span>
      <span className="flex flex-col items-end">
        <span className="tabular font-semibold text-sm">
          {formatPoints(o.price.pointsPerPassenger)}
        </span>
        <span className="tabular text-muted-foreground text-[11px]">
          +{" "}
          {formatMoney(
            o.price.taxesPerPassenger.amount,
            o.price.taxesPerPassenger.currency,
          )}
        </span>
      </span>
    </button>
  );
}
