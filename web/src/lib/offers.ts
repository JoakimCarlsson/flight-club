import type { Offer } from "@/lib/chat";

/** The rankings the results can be ordered by. */
export type Ranking = "best" | "cheapest" | "fastest";

/** Names of the airlines and programs the results mention, by code. */
const AIRLINES: Record<string, string> = {
  AS: "Alaska",
  AA: "American",
  AY: "Finnair",
  BA: "British Airways",
  QR: "Qatar Airways",
  IB: "Iberia",
  JL: "Japan Airlines",
  CX: "Cathay Pacific",
  HA: "Hawaiian",
  QF: "Qantas",
  FJ: "Fiji Airways",
  EI: "Aer Lingus",
  LA: "LATAM",
  KE: "Korean Air",
  UA: "United",
  DL: "Delta",
  AC: "Air Canada",
  TK: "Turkish",
  NH: "ANA",
  AF: "Air France",
  SK: "SAS",
  VS: "Virgin Atlantic",
  B6: "JetBlue",
  X1: "SAS Connect",
  KL: "KLM",
};

/** Display names of the loyalty programs, by key. */
const PROGRAMS: Record<string, string> = {
  alaska: "Alaska Atmos",
  sas: "SAS EuroBonus",
  virgin: "Virgin Flying Club",
  jetblue: "JetBlue TrueBlue",
};

/** The airline that runs each loyalty program, by program key. */
const PROGRAM_AIRLINES: Record<string, string> = {
  alaska: "AS",
  sas: "SK",
  virgin: "VS",
  jetblue: "B6",
};

/**
 * Names the airline whose logo stands for an offer: the first operating
 * carrier, or the program's own airline when the offer has no flights.
 *
 * @param o - The offer.
 * @returns An IATA airline code.
 */
export function logoCarrier(o: Offer): string {
  return o.segments[0]?.operatingCarrier ?? PROGRAM_AIRLINES[o.program] ?? "?";
}

/**
 * Reports whether an offer is a day's lowest price rather than a flight,
 * as programs that only publish a calendar give.
 *
 * @param o - The offer.
 * @returns True when the offer has no flights.
 */
export function isDailyPrice(o: Offer): boolean {
  return o.segments.length === 0;
}

/**
 * Names an airline by its IATA code.
 *
 * @param code - The two-letter code.
 * @returns The airline's name, or the code when it is not known.
 */
export function airlineName(code: string): string {
  return AIRLINES[code] ?? code;
}

/**
 * Names a loyalty program by its key.
 *
 * @param key - The program key, e.g. "alaska".
 * @returns The program's display name.
 */
export function programName(key: string): string {
  return PROGRAMS[key] ?? key.charAt(0).toUpperCase() + key.slice(1);
}

/**
 * Scores an offer for the "best" ranking: points, plus a cost for every hour
 * travelled and every stop, so a slightly dearer nonstop beats a cheap
 * overnight connection. Lower is better.
 *
 * @param o - The offer.
 * @returns The score.
 */
function bestScore(o: Offer): number {
  return (
    o.price.pointsPerPassenger / 1000 + o.durationMinutes / 40 + o.stops * 4
  );
}

/**
 * Orders offers by a ranking, without changing the input.
 *
 * @param offers - The offers.
 * @param ranking - How to order them.
 * @returns A new, ordered array.
 */
export function rank(offers: Offer[], ranking: Ranking): Offer[] {
  const key: (o: Offer) => number =
    ranking === "cheapest"
      ? (o) => o.price.pointsPerPassenger * 1e4 + o.durationMinutes
      : ranking === "fastest"
        ? (o) => o.durationMinutes * 1e6 + o.price.pointsPerPassenger
        : bestScore;
  return [...offers].sort((a, b) => key(a) - key(b));
}

/**
 * Identifies an offer across renders and rankings.
 *
 * @param o - The offer.
 * @returns A key unique to its flights, fare and direction.
 */
export function offerKey(o: Offer): string {
  return `${o.direction}|${o.fare}|${o.segments.map((s) => s.flight + s.departure).join(",")}`;
}

/**
 * Reads the local date of a timestamp without converting it to the
 * reader's zone.
 *
 * @param iso - An RFC 3339 timestamp with an offset.
 * @returns YYYY-MM-DD.
 */
export function localDate(iso: string): string {
  return iso.slice(0, 10);
}

/**
 * Reads the local time of a timestamp without converting it to the
 * reader's zone.
 *
 * @param iso - An RFC 3339 timestamp with an offset.
 * @returns HH:MM.
 */
export function localTime(iso: string): string {
  return iso.slice(11, 16);
}

/**
 * Counts the days an arrival lands after its departure, in local dates.
 *
 * @param o - The offer.
 * @returns 0 for a same-day arrival, 1 for the next day and so on.
 */
export function dayOffset(o: Offer): number {
  const from = Date.parse(localDate(o.departure));
  const to = Date.parse(localDate(o.arrival));
  return Math.round((to - from) / 86_400_000);
}

/**
 * Formats a date as a weekday, day and month.
 *
 * @param date - YYYY-MM-DD.
 * @param parts - Which parts to include.
 * @returns E.g. "Tue 15 Dec".
 */
export function formatDate(
  date: string,
  parts: { weekday?: boolean; month?: boolean } = {
    weekday: true,
    month: true,
  },
): string {
  const d = new Date(`${date}T12:00:00Z`);
  return d.toLocaleDateString("en-GB", {
    timeZone: "UTC",
    day: "numeric",
    ...(parts.weekday ? { weekday: "short" } : {}),
    ...(parts.month ? { month: "short" } : {}),
  });
}

/**
 * Formats a date range, collapsing a single day.
 *
 * @param from - The first date, YYYY-MM-DD.
 * @param to - The last date, YYYY-MM-DD.
 * @returns E.g. "15 – 17 Dec".
 */
export function formatRange(from: string, to: string): string {
  if (from === to) return formatDate(from);
  return `${formatDate(from, { month: from.slice(5, 7) !== to.slice(5, 7) })} – ${formatDate(to, { month: true })}`;
}

/**
 * Formats minutes as hours and minutes.
 *
 * @param minutes - The duration.
 * @returns E.g. "5h 20m".
 */
export function formatDuration(minutes: number): string {
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return h === 0 ? `${m}m` : m === 0 ? `${h}h` : `${h}h ${m}m`;
}

/**
 * Formats a points price in full.
 *
 * @param points - The points.
 * @returns E.g. "12,500".
 */
export function formatPoints(points: number): string {
  return points.toLocaleString("en-US");
}

/**
 * Formats a points price compactly.
 *
 * @param points - The points.
 * @returns E.g. "12.5k".
 */
export function formatPointsShort(points: number): string {
  if (points < 1000) return String(points);
  const k = points / 1000;
  return `${Number.isInteger(k) ? k : k.toFixed(1)}k`;
}

/**
 * Formats a cash amount, without cents when it is whole.
 *
 * @param amount - The amount in the major unit.
 * @param currency - The ISO 4217 code.
 * @returns E.g. "$5.60" or "$186".
 */
export function formatMoney(amount: number, currency: string): string {
  return amount.toLocaleString("en-US", {
    style: "currency",
    currency,
    minimumFractionDigits: Number.isInteger(amount) ? 0 : 2,
  });
}

/**
 * Describes the stops of an offer.
 *
 * @param o - The offer.
 * @returns E.g. "Nonstop" or "1 stop · SFO".
 */
export function describeStops(o: Offer): string {
  if (o.stops === 0) return "Nonstop";
  const via = o.segments
    .slice(0, -1)
    .map((s) => s.destination)
    .join(", ");
  return `${o.stops} stop${o.stops > 1 ? "s" : ""} · ${via}`;
}
