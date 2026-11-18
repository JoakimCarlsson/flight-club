import { ChevronDown, ChevronRight, ExternalLink, Plane } from "lucide-react";
import { Fragment, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Run } from "@/hooks/use-chat";
import type { Cabin, Offer, SearchView, Step } from "@/lib/chat";
import { cn } from "@/lib/utils";

/**
 * Renders what the assistant is doing: the search it understood, its
 * progress and its results.
 *
 * @param props.run - The search to show, if any.
 * @returns The workspace.
 */
export function Workspace({ run }: { run?: Run }) {
  if (!run) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
        <Plane className="size-8" />
        <p className="text-sm">Searches the assistant runs show up here.</p>
      </div>
    );
  }
  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-6">
      {run.search && <Understood search={run.search} />}
      <Progress steps={Object.values(run.steps)} />
      {run.results ? (
        <Results offers={run.results.offers} />
      ) : run.failed ? (
        <p className="text-destructive text-sm">The search could not run.</p>
      ) : null}
    </div>
  );
}

/**
 * Renders a titled section.
 *
 * @param props.title - The title.
 * @param props.children - The content.
 * @returns The section.
 */
function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-xl border bg-card p-4">
      <h2 className="mb-3 font-medium text-muted-foreground text-xs uppercase tracking-wide">
        {title}
      </h2>
      {children}
    </section>
  );
}

/**
 * Renders the search as the assistant understood it.
 *
 * @param props.search - The search.
 * @returns The section.
 */
function Understood({ search }: { search: SearchView }) {
  const chips = [
    `${search.origin} → ${search.destination}`,
    `Depart ${range(search.departFrom, search.departTo)}`,
    search.returnFrom
      ? `Return ${range(search.returnFrom, search.returnTo ?? search.returnFrom)}`
      : "One-way",
    `${search.passengers} passenger${search.passengers === 1 ? "" : "s"}`,
    search.cabins.length ? search.cabins.join(", ") : "Any cabin",
    `Programs: ${search.programs.join(", ")}`,
  ];
  return (
    <Section title="What the assistant understood">
      <div className="flex flex-wrap gap-2">
        {chips.map((c) => (
          <Badge key={c} variant="secondary" className="font-normal">
            {c}
          </Badge>
        ))}
      </div>
    </Section>
  );
}

/**
 * Renders every program-day of the search as a cell that fills as it runs.
 *
 * @param props.steps - The program-days.
 * @returns The section, or nothing before the search starts.
 */
function Progress({ steps }: { steps: Step[] }) {
  if (steps.length === 0) return null;
  const rows = [...new Set(steps.map((s) => `${s.program}|${s.direction}`))];
  const done = steps.filter((s) => s.state !== "running").length;
  return (
    <Section title={`Progress · ${done} of ${steps.length}`}>
      <div className="flex flex-col gap-2">
        {rows.map((row) => {
          const [program, direction] = row.split("|");
          const cells = steps
            .filter((s) => `${s.program}|${s.direction}` === row)
            .sort((a, b) => a.date.localeCompare(b.date));
          return (
            <div key={row} className="flex items-center gap-3 text-sm">
              <span className="w-36 shrink-0">
                {program}{" "}
                <span className="text-muted-foreground">{direction}</span>
              </span>
              <div className="flex flex-1 gap-1">
                {cells.map((s) => (
                  <div
                    key={s.date}
                    title={s.error ?? `${s.date}: ${s.offers} offers`}
                    className={cn(
                      "flex h-7 flex-1 items-center justify-center rounded text-xs",
                      s.state === "running" &&
                        "animate-pulse bg-muted text-muted-foreground",
                      s.state === "done" &&
                        "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
                      s.state === "failed" &&
                        "bg-destructive/15 text-destructive",
                    )}
                  >
                    {shortDate(s.date)}
                    {s.state === "done" && ` · ${s.offers}`}
                  </div>
                ))}
              </div>
            </div>
          );
        })}
      </div>
    </Section>
  );
}

/** The orders the results can be sorted in. */
type SortKey = "points" | "departure" | "duration";

/**
 * Renders the offers, filterable by direction and cabin and sortable, with
 * a row that opens to show each flight.
 *
 * @param props.offers - The offers.
 * @returns The section.
 */
function Results({ offers }: { offers: Offer[] }) {
  const [direction, setDirection] = useState<"outbound" | "return">("outbound");
  const [cabin, setCabin] = useState<Cabin | "all">("all");
  const [sort, setSort] = useState<SortKey>("points");
  const [open, setOpen] = useState<number | null>(null);

  const hasReturn = offers.some((o) => o.direction === "return");
  const cabins = [...new Set(offers.map((o) => o.cabin))];
  const shown = useMemo(
    () =>
      offers
        .filter((o) => o.direction === direction)
        .filter((o) => cabin === "all" || o.cabin === cabin)
        .sort((a, b) => {
          if (sort === "departure")
            return a.departure.localeCompare(b.departure);
          if (sort === "duration") return a.durationMinutes - b.durationMinutes;
          return a.price.pointsPerPassenger - b.price.pointsPerPassenger;
        }),
    [offers, direction, cabin, sort],
  );

  return (
    <Section title={`Results · ${offers.length} offers`}>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        {hasReturn &&
          (["outbound", "return"] as const).map((d) => (
            <Button
              key={d}
              size="sm"
              variant={direction === d ? "default" : "outline"}
              onClick={() => setDirection(d)}
            >
              {d}
            </Button>
          ))}
        <span className="mx-1 h-5 w-px bg-border" />
        {(["all", ...cabins] as const).map((c) => (
          <Button
            key={c}
            size="sm"
            variant={cabin === c ? "secondary" : "ghost"}
            onClick={() => setCabin(c)}
          >
            {c}
          </Button>
        ))}
        <span className="ml-auto text-muted-foreground text-xs">Sort</span>
        {(["points", "departure", "duration"] as const).map((k) => (
          <Button
            key={k}
            size="sm"
            variant={sort === k ? "secondary" : "ghost"}
            onClick={() => setSort(k)}
          >
            {k}
          </Button>
        ))}
      </div>

      {shown.length === 0 ? (
        <p className="py-6 text-center text-muted-foreground text-sm">
          No offers match.
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-6" />
              <TableHead>Flights</TableHead>
              <TableHead>Departs</TableHead>
              <TableHead>Arrives</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead>Cabin</TableHead>
              <TableHead className="text-right">Points</TableHead>
              <TableHead className="text-right">Taxes</TableHead>
              <TableHead className="text-right">Seats</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((o, i) => (
              <Fragment key={i}>
                <TableRow
                  className="cursor-pointer"
                  onClick={() => setOpen(open === i ? null : i)}
                >
                  <TableCell>
                    {open === i ? (
                      <ChevronDown className="size-4" />
                    ) : (
                      <ChevronRight className="size-4" />
                    )}
                  </TableCell>
                  <TableCell>
                    <div className="font-medium">
                      {o.segments.map((s) => s.flight).join(" · ")}
                    </div>
                    <div className="text-muted-foreground text-xs">
                      {[o.origin, ...o.segments.map((s) => s.destination)].join(
                        " → ",
                      )}{" "}
                      · {o.program}
                    </div>
                  </TableCell>
                  <TableCell>{localTime(o.departure)}</TableCell>
                  <TableCell>{localTime(o.arrival)}</TableCell>
                  <TableCell>
                    {duration(o.durationMinutes)}
                    <div className="text-muted-foreground text-xs">
                      {o.stops === 0
                        ? "nonstop"
                        : `${o.stops} stop${o.stops > 1 ? "s" : ""}`}
                    </div>
                  </TableCell>
                  <TableCell>
                    {o.cabin}
                    {o.mixedCabin && (
                      <div className="text-muted-foreground text-xs">mixed</div>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="font-medium">
                      {o.price.pointsPerPassenger.toLocaleString()}
                    </div>
                    <div className="text-muted-foreground text-xs">
                      {o.price.pointsTotal.toLocaleString()} total
                    </div>
                  </TableCell>
                  <TableCell className="text-right">
                    {money(
                      o.price.taxesPerPassenger.amount,
                      o.price.taxesPerPassenger.currency,
                    )}
                    <div className="text-muted-foreground text-xs">
                      {money(
                        o.price.taxesTotal.amount,
                        o.price.taxesTotal.currency,
                      )}{" "}
                      total
                    </div>
                  </TableCell>
                  <TableCell className="text-right">{o.seats ?? "–"}</TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        o.availability === "confirmed" ? "default" : "secondary"
                      }
                    >
                      {o.availability}
                    </Badge>
                  </TableCell>
                </TableRow>
                {open === i && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell />
                    <TableCell colSpan={9}>
                      <OfferDetail offer={o} />
                    </TableCell>
                  </TableRow>
                )}
              </Fragment>
            ))}
          </TableBody>
        </Table>
      )}
    </Section>
  );
}

/**
 * Renders the flights of an offer, its notes and where to book it.
 *
 * @param props.offer - The offer.
 * @returns The detail.
 */
function OfferDetail({ offer }: { offer: Offer }) {
  return (
    <div className="flex flex-col gap-3 py-2">
      {offer.segments.map((s) => (
        <div
          key={s.flight + s.departure}
          className="flex flex-col gap-1 text-sm"
        >
          <div className="flex gap-3">
            <span className="w-20 font-medium">{s.flight}</span>
            <span>
              {s.origin} {localTime(s.departure)} → {s.destination}{" "}
              {localTime(s.arrival)}
            </span>
            <span className="text-muted-foreground">
              {duration(s.durationMinutes)} · {s.aircraft} · {s.cabin} (
              {s.bookingCode})
              {s.operatingCarrier !== s.marketingCarrier &&
                ` · operated by ${s.operatingCarrier}`}
            </span>
          </div>
          {s.layoverMinutes > 0 && (
            <div className="pl-[5.75rem] text-muted-foreground text-xs">
              {duration(s.layoverMinutes)} connection in {s.destination}
            </div>
          )}
        </div>
      ))}
      {offer.notes && offer.notes.length > 0 && (
        <ul className="list-disc pl-5 text-muted-foreground text-xs">
          {offer.notes.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      )}
      <div className="flex items-center gap-3 text-muted-foreground text-xs">
        <span>Fare {offer.fare}</span>
        {offer.bookingUrl && (
          <Button asChild size="sm" variant="outline">
            <a href={offer.bookingUrl} target="_blank" rel="noreferrer">
              Book on {offer.program} <ExternalLink className="size-3" />
            </a>
          </Button>
        )}
      </div>
    </div>
  );
}

/**
 * Formats a date range, collapsing a single day.
 *
 * @param from - The first date, YYYY-MM-DD.
 * @param to - The last date, YYYY-MM-DD.
 * @returns E.g. "Dec 15 – Dec 17".
 */
function range(from: string, to: string): string {
  return from === to
    ? shortDate(from)
    : `${shortDate(from)} – ${shortDate(to)}`;
}

/**
 * Formats a date as a short month and day.
 *
 * @param date - YYYY-MM-DD.
 * @returns E.g. "Dec 15".
 */
function shortDate(date: string): string {
  const [, month = "1", day = "1"] = date.split("-");
  const name = new Date(2000, Number(month) - 1).toLocaleString("en", {
    month: "short",
  });
  return `${name} ${Number(day)}`;
}

/**
 * Formats a timestamp as the local date and time it carries, without
 * converting it to the reader's zone.
 *
 * @param iso - An RFC 3339 timestamp with an offset.
 * @returns E.g. "Dec 15 06:00".
 */
function localTime(iso: string): string {
  const [date = "", time = ""] = iso.split("T");
  return `${shortDate(date)} ${time.slice(0, 5)}`;
}

/**
 * Formats minutes as hours and minutes.
 *
 * @param minutes - The duration.
 * @returns E.g. "5h 20m".
 */
function duration(minutes: number): string {
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

/**
 * Formats an amount in a currency.
 *
 * @param amount - The amount in the major unit.
 * @param currency - The ISO 4217 code.
 * @returns E.g. "$11.20".
 */
function money(amount: number, currency: string): string {
  return amount.toLocaleString("en", { style: "currency", currency });
}
