/** A cabin of service, the same across programs. */
export type Cabin = "economy" | "premium" | "business" | "first";

/** An amount in a currency. */
export interface Money {
  amount: number;
  currency: string;
}

/** One flight of an itinerary. */
export interface Segment {
  flight: string;
  marketingCarrier: string;
  operatingCarrier: string;
  origin: string;
  destination: string;
  departure: string;
  arrival: string;
  durationMinutes: number;
  layoverMinutes: number;
  aircraft: string;
  cabin: Cabin;
  bookingCode: string;
}

/** One itinerary a program sells for points, in one fare. */
export interface Offer {
  program: string;
  direction: "outbound" | "return";
  availability: "confirmed" | "estimated" | "unavailable";
  origin: string;
  destination: string;
  departure: string;
  arrival: string;
  durationMinutes: number;
  stops: number;
  airlines: string[];
  cabin: Cabin;
  mixedCabin: boolean;
  fare: string;
  price: {
    pointsPerPassenger: number;
    pointsTotal: number;
    taxesPerPassenger: Money;
    taxesTotal: Money;
  };
  seats: number | null;
  bookingUrl?: string;
  notes?: string[];
  segments: Segment[];
}

/** One program-day that failed during a search. */
export interface ProgramError {
  program: string;
  direction: string;
  date: string;
  message: string;
}

/** What a search across programs found. */
export interface AwardResult {
  offers: Offer[];
  errors: ProgramError[];
}

/** A search as the assistant understood it. */
export interface SearchView {
  origin: string;
  destination: string;
  departFrom: string;
  departTo: string;
  returnFrom?: string;
  returnTo?: string;
  passengers: number;
  cabins: Cabin[];
  programs: string[];
}

/** One program-day of a search starting or finishing. */
export interface Step {
  program: string;
  direction: "outbound" | "return";
  date: string;
  state: "running" | "done" | "failed";
  offers: number;
  found?: Offer[];
  error?: string;
}

/** One message of the conversation, as the server takes it. */
export interface Turn {
  role: "user" | "assistant";
  text: string;
}

/** One frame of the chat stream; the event name says which fields are set. */
export interface ChatEvent {
  event:
    | "text"
    | "tool"
    | "toolResult"
    | "search"
    | "step"
    | "results"
    | "done"
    | "error";
  text?: string;
  callId?: string;
  tool?: string;
  input?: string;
  failed?: boolean;
  search?: SearchView;
  step?: Step;
  results?: AwardResult;
  error?: string;
}

/**
 * Sends the conversation and reports every event of the assistant's turn as
 * it arrives.
 *
 * @param messages - The conversation so far, ending with the new user message.
 * @param onEvent - Called with each event, in order.
 * @param signal - Aborts the turn.
 * @throws Error with the API's problem detail when the turn cannot start.
 */
export async function streamChat(
  messages: Turn[],
  onEvent: (event: ChatEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await fetch("/api/chat/stream", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ messages }),
    signal,
  });
  if (!res.ok || !res.body) {
    const problem = await res.json().catch(() => null);
    throw new Error(problem?.detail ?? `Chat failed (${res.status})`);
  }

  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += value;
    let end = buffer.indexOf("\n\n");
    while (end !== -1) {
      const frame = parseFrame(buffer.slice(0, end));
      buffer = buffer.slice(end + 2);
      if (frame) onEvent(frame);
      end = buffer.indexOf("\n\n");
    }
  }
}

/**
 * Parses one Server-Sent Events frame.
 *
 * @param block - The frame's lines, without the blank line ending it.
 * @returns The event, or null for a frame with no event name.
 */
function parseFrame(block: string): ChatEvent | null {
  let name = "";
  const data: string[] = [];
  for (const line of block.split("\n")) {
    if (line.startsWith("event:")) name = line.slice(6).trim();
    else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  }
  if (!name) return null;
  const payload = data.length ? JSON.parse(data.join("\n")) : {};
  return { ...payload, event: name } as ChatEvent;
}
