import {
  ArrowRight,
  Check,
  ExternalLink,
  Loader2,
  PlaneTakeoff,
  TriangleAlert,
} from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useMemo, useState } from "react";
import { AirlineLogo } from "@/components/airline-logo";
import type { Run } from "@/hooks/use-chat";
import type { Cabin, Offer, SearchView, Step } from "@/lib/chat";
import {
  airlineName,
  dayOffset,
  describeStops,
  formatDate,
  formatDuration,
  formatMoney,
  formatPoints,
  formatPointsShort,
  formatRange,
  localDate,
  localTime,
  offerKey,
  programName,
  type Ranking,
  rank,
} from "@/lib/offers";
import { cn } from "@/lib/utils";

/** How many offers are listed before "Show all". */
const PAGE = 25;

/**
 * Renders what the assistant is doing: the trip it understood, the search
 * as it runs and the offers as they arrive.
 *
 * @param props.run - The search to show, if any.
 * @param props.focus - The offer the chat asked to show, by offerKey.
 * @returns The workspace.
 */
export function Workspace({ run, focus }: { run?: Run; focus?: string }) {
  if (!run?.search) return <EmptyWorkspace searching={!!run} />;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 px-8 py-8">
        <TripHeader search={run.search} run={run} />
        <Results run={run} search={run.search} focus={focus} />
      </div>
    </div>
  );
}

/**
 * Renders the workspace before any search: what will appear here.
 *
 * @param props.searching - Set while the assistant is starting a search.
 * @returns The empty workspace.
 */
function EmptyWorkspace({ searching }: { searching: boolean }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 px-8 text-center">
      <div className="flex size-14 items-center justify-center rounded-2xl bg-brand/10 text-brand">
        {searching ? (
          <Loader2 className="size-6 animate-spin" />
        ) : (
          <PlaneTakeoff className="size-6" />
        )}
      </div>
      <div className="space-y-1">
        <p className="font-medium text-lg tracking-tight">
          {searching ? "Starting the search…" : "Flights show up here"}
        </p>
        <p className="max-w-sm text-muted-foreground text-sm">
          Ask in the chat. Every flight the assistant finds lands here, live,
          with points, taxes and seats left.
        </p>
      </div>
    </div>
  );
}

/**
 * Renders the trip as the title, what was understood under it, and the
 * search's live status.
 *
 * @param props.search - The search as understood.
 * @param props.run - The search's progress.
 * @returns The header.
 */
function TripHeader({ search, run }: { search: SearchView; run: Run }) {
  const meta = [
    formatRange(search.departFrom, search.departTo),
    search.returnFrom
      ? `return ${formatRange(search.returnFrom, search.returnTo ?? search.returnFrom)}`
      : "one-way",
    `${search.passengers} ${search.passengers === 1 ? "passenger" : "passengers"}`,
    search.cabins.length ? search.cabins.join(" or ") : "any cabin",
    search.programs.map(programName).join(", "),
  ];
  return (
    <header className="flex flex-col gap-3">
      <h1 className="flex items-center gap-3 font-semibold text-3xl tracking-tight">
        {search.origin}
        <ArrowRight className="size-6 text-muted-foreground" />
        {search.destination}
      </h1>
      <p className="text-muted-foreground text-sm">{meta.join(" · ")}</p>
      <LiveStatus run={run} search={search} />
    </header>
  );
}

/**
 * Says what the search is doing right now, in words: which program and day
 * it is checking, then what it found.
 *
 * @param props.run - The search's progress.
 * @param props.search - The search as understood.
 * @returns The status line.
 */
function LiveStatus({ run, search }: { run: Run; search: SearchView }) {
  const steps = Object.values(run.steps);
  const running = steps.filter((s) => s.state === "running");
  const failed = steps.filter((s) => s.state === "failed");
  const done = !!run.results || run.failed;

  let text: string;
  if (run.failed) text = "The search could not run.";
  else if (done)
    text = `Searched ${steps.length} ${steps.length === 1 ? "day" : "days"} · ${run.offers.length} offers found`;
  else if (running.length > 0) {
    const s = running[0]!;
    text = `Checking ${programName(s.program)} · ${formatDate(s.date)} · ${search.passengers} ${search.passengers === 1 ? "seat" : "seats"}${running.length > 1 ? ` · ${running.length - 1} more in flight` : ""}`;
  } else text = "Planning the search…";

  return (
    <div className="flex flex-col gap-2">
      <div
        className={cn(
          "inline-flex w-fit items-center gap-2 rounded-full border px-3 py-1 text-sm",
          done && !run.failed && "text-muted-foreground",
          run.failed && "border-destructive/40 text-destructive",
        )}
      >
        {done ? (
          run.failed ? (
            <TriangleAlert className="size-3.5" />
          ) : (
            <Check className="size-3.5 text-cheap" />
          )
        ) : (
          <span className="relative flex size-2">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-brand opacity-60" />
            <span className="relative inline-flex size-2 rounded-full bg-brand" />
          </span>
        )}
        <AnimatePresence mode="wait" initial={false}>
          <motion.span
            key={text}
            initial={{ opacity: 0, y: 4 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -4 }}
            transition={{ duration: 0.18 }}
          >
            {text}
          </motion.span>
        </AnimatePresence>
      </div>
      {failed.length > 0 && (
        <ul className="text-destructive text-xs">
          {failed.map((s) => (
            <li key={`${s.program}${s.direction}${s.date}`}>
              {programName(s.program)} · {formatDate(s.date)}: {s.error}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Renders the offers: a direction switch, a strip of days with the lowest
 * price on each, the rankings and filters, and the list.
 *
 * @param props.run - The search's progress and offers.
 * @param props.search - The search as understood.
 * @param props.focus - The offer the chat asked to show, by offerKey.
 * @returns The results.
 */
function Results({
  run,
  search,
  focus,
}: {
  run: Run;
  search: SearchView;
  focus?: string;
}) {
  const [direction, setDirection] = useState<"outbound" | "return">("outbound");
  const [day, setDay] = useState<string | null>(null);
  const [ranking, setRanking] = useState<Ranking>("best");
  const [cabin, setCabin] = useState<Cabin | null>(null);
  const [nonstop, setNonstop] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const [limit, setLimit] = useState(PAGE);

  useEffect(() => {
    if (!focus) return;
    const target = run.offers.find((o) => offerKey(o) === focus);
    if (!target) return;
    setDirection(target.direction);
    setDay(null);
    setCabin(null);
    setNonstop(false);
    setOpen(focus);
    setLimit(Number.POSITIVE_INFINITY);
    requestAnimationFrame(() =>
      document
        .getElementById(`offer-${focus}`)
        ?.scrollIntoView({ behavior: "smooth", block: "center" }),
    );
  }, [focus, run.offers]);

  const days = useMemo(() => {
    const from =
      direction === "outbound" ? search.departFrom : search.returnFrom;
    const to = direction === "outbound" ? search.departTo : search.returnTo;
    return from ? dateRange(from, to ?? from) : [];
  }, [direction, search]);

  const inDirection = run.offers.filter(
    (o) =>
      o.direction === direction &&
      (!cabin || o.cabin === cabin) &&
      (!nonstop || o.stops === 0),
  );
  const cabins = [...new Set(run.offers.map((o) => o.cabin))].sort(
    (a, b) => CABIN_ORDER.indexOf(a) - CABIN_ORDER.indexOf(b),
  );
  const shown = rank(
    inDirection.filter((o) => !day || localDate(o.departure) === day),
    ranking,
  );

  return (
    <section className="flex flex-col gap-4">
      {search.returnFrom && (
        <div className="inline-flex w-fit rounded-lg bg-muted p-1">
          {(["outbound", "return"] as const).map((d) => (
            <button
              key={d}
              type="button"
              onClick={() => {
                setDirection(d);
                setDay(null);
              }}
              className={cn(
                "rounded-md px-4 py-1.5 font-medium text-sm transition-colors",
                direction === d
                  ? "bg-background shadow-sm"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {d === "outbound"
                ? `${search.origin} → ${search.destination}`
                : `${search.destination} → ${search.origin}`}
            </button>
          ))}
        </div>
      )}

      <DateStrip
        days={days}
        offers={inDirection}
        steps={Object.values(run.steps).filter(
          (s) => s.direction === direction,
        )}
        selected={day}
        onSelect={setDay}
      />

      <div className="flex flex-wrap items-center gap-x-6 gap-y-3 border-b">
        {(["best", "cheapest", "fastest"] as const).map((r) => (
          <button
            key={r}
            type="button"
            onClick={() => setRanking(r)}
            className={cn(
              "-mb-px border-b-2 pb-2.5 font-medium text-sm capitalize transition-colors",
              ranking === r
                ? "border-brand text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {r}
          </button>
        ))}
        <div className="ml-auto flex items-center gap-1.5 pb-2">
          {cabins.length > 1 &&
            cabins.map((c) => (
              <Chip
                key={c}
                active={cabin === c}
                onClick={() => setCabin(cabin === c ? null : c)}
              >
                {c}
              </Chip>
            ))}
          <Chip active={nonstop} onClick={() => setNonstop(!nonstop)}>
            Nonstop
          </Chip>
        </div>
      </div>

      {shown.length === 0 ? (
        <p className="py-12 text-center text-muted-foreground text-sm">
          {run.results || run.failed
            ? "No flights match these filters."
            : "Flights appear here as each day comes back."}
        </p>
      ) : (
        <ul className="flex flex-col">
          <AnimatePresence initial={false}>
            {shown.slice(0, limit).map((o, i) => {
              const key = offerKey(o);
              return (
                <motion.li
                  key={key}
                  id={`offer-${key}`}
                  layout="position"
                  initial={{ opacity: 0, y: 8 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.22, delay: Math.min(i, 10) * 0.02 }}
                  className="border-b last:border-b-0"
                >
                  <OfferRow
                    offer={o}
                    passengers={search.passengers}
                    highlight={i === 0 ? ranking : undefined}
                    open={open === key}
                    onToggle={() => setOpen(open === key ? null : key)}
                  />
                </motion.li>
              );
            })}
          </AnimatePresence>
        </ul>
      )}
      {shown.length > limit && (
        <button
          type="button"
          onClick={() => setLimit(Number.POSITIVE_INFINITY)}
          className="self-center rounded-full border px-4 py-1.5 text-muted-foreground text-sm hover:text-foreground"
        >
          Show all {shown.length} flights
        </button>
      )}
    </section>
  );
}

/** The cabins from cheapest to dearest, for ordering filters. */
const CABIN_ORDER: Cabin[] = ["economy", "premium", "business", "first"];

/**
 * Renders a small toggle chip.
 *
 * @param props.active - Set when it is on.
 * @param props.onClick - Toggles it.
 * @param props.children - Its label.
 * @returns The chip.
 */
function Chip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded-full border px-3 py-1 text-xs capitalize transition-colors",
        active
          ? "border-foreground bg-foreground text-background"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

/**
 * Renders one tile per day with the lowest points on it, the cheapest day
 * in green and the dearest in red. A tile filters the list to its day.
 *
 * @param props.days - The days searched, YYYY-MM-DD.
 * @param props.offers - The offers of the direction shown, filtered.
 * @param props.steps - The program-days of that direction.
 * @param props.selected - The day filtered to, or null for every day.
 * @param props.onSelect - Filters to a day, or to every day with null.
 * @returns The strip, or nothing for a single day.
 */
function DateStrip({
  days,
  offers,
  steps,
  selected,
  onSelect,
}: {
  days: string[];
  offers: Offer[];
  steps: Step[];
  selected: string | null;
  onSelect: (day: string | null) => void;
}) {
  if (days.length < 2) return null;
  const lowest = new Map<string, number>();
  for (const o of offers) {
    const d = localDate(o.departure);
    const p = o.price.pointsPerPassenger;
    if (!lowest.has(d) || p < lowest.get(d)!) lowest.set(d, p);
  }
  const prices = [...lowest.values()];
  const min = Math.min(...prices);
  const max = Math.max(...prices);

  return (
    <div className="grid auto-cols-fr grid-flow-col gap-2">
      {days.map((d) => {
        const price = lowest.get(d);
        const pending = steps.some(
          (s) => s.date === d && s.state === "running",
        );
        const active = selected === d;
        return (
          <button
            key={d}
            type="button"
            onClick={() => onSelect(active ? null : d)}
            className={cn(
              "flex flex-col items-start gap-1 rounded-xl border px-3 py-2.5 text-left transition-colors hover:bg-muted/60",
              active && "border-foreground ring-1 ring-foreground",
            )}
          >
            <span className="text-muted-foreground text-xs">
              {formatDate(d)}
            </span>
            {pending ? (
              <span className="h-5 w-14 animate-pulse rounded bg-muted" />
            ) : price === undefined ? (
              <span className="text-muted-foreground text-sm">—</span>
            ) : (
              <span
                className={cn(
                  "tabular font-semibold text-sm",
                  prices.length > 1 && price === min && "text-cheap",
                  prices.length > 1 &&
                    price === max &&
                    min !== max &&
                    "text-dear",
                )}
              >
                {formatPointsShort(price)}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

/** What the first row is marked with under each ranking. */
const HIGHLIGHT: Record<Ranking, string> = {
  best: "Best",
  cheapest: "Cheapest",
  fastest: "Fastest",
};

/**
 * Renders one offer as a flat row that leads with the times, with the
 * points on the right; the whole row opens the detail.
 *
 * @param props.offer - The offer.
 * @param props.passengers - How many passengers the search was for.
 * @param props.highlight - The ranking this row tops, if it is first.
 * @param props.open - Set when the detail is shown.
 * @param props.onToggle - Opens or closes the detail.
 * @returns The row.
 */
function OfferRow({
  offer: o,
  passengers,
  highlight,
  open,
  onToggle,
}: {
  offer: Offer;
  passengers: number;
  highlight?: Ranking;
  open: boolean;
  onToggle: () => void;
}) {
  const plus = dayOffset(o);
  return (
    <div className={cn("transition-colors", open && "bg-muted/40")}>
      <button
        type="button"
        onClick={onToggle}
        className="grid w-full grid-cols-[auto_1fr_auto] items-center gap-5 px-2 py-4 text-left hover:bg-muted/40"
      >
        <AirlineLogo code={o.segments[0]?.operatingCarrier ?? "?"} />

        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex items-baseline gap-2">
            <span className="tabular font-semibold text-lg tracking-tight">
              {localTime(o.departure)} – {localTime(o.arrival)}
            </span>
            {plus > 0 && (
              <span className="font-medium text-dear text-xs">+{plus}</span>
            )}
            {highlight && (
              <span className="rounded-full bg-brand/15 px-2 py-0.5 font-medium text-brand text-xs">
                {HIGHLIGHT[highlight]}
              </span>
            )}
          </div>
          <div className="truncate text-muted-foreground text-sm">
            {o.airlines.map(airlineName).join(", ")} ·{" "}
            {formatDate(localDate(o.departure))} ·{" "}
            <span className="font-mono text-xs">
              {o.segments.map((s) => s.flight).join(" ")}
            </span>
          </div>
        </div>

        <div className="flex items-center gap-8">
          <div className="hidden w-36 flex-col gap-1 md:flex">
            <RouteLine offer={o} />
            <span className="text-muted-foreground text-xs">
              {formatDuration(o.durationMinutes)} · {describeStops(o)}
            </span>
          </div>
          <span className="hidden w-20 text-muted-foreground text-sm capitalize lg:block">
            {o.cabin}
            {o.mixedCabin && <span className="block text-xs">mixed</span>}
          </span>
          <div className="flex w-28 flex-col items-end">
            <span className="tabular font-semibold text-lg tracking-tight">
              {formatPoints(o.price.pointsPerPassenger)}
            </span>
            <span className="tabular text-muted-foreground text-xs">
              +{" "}
              {formatMoney(
                o.price.taxesPerPassenger.amount,
                o.price.taxesPerPassenger.currency,
              )}
              {passengers > 1 ? " pp" : ""}
            </span>
          </div>
        </div>
      </button>

      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.2 }}
            className="overflow-hidden"
          >
            <OfferDetail offer={o} passengers={passengers} />
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

/**
 * Draws an itinerary as a line with a dot per connection.
 *
 * @param props.offer - The offer.
 * @returns The line.
 */
function RouteLine({ offer }: { offer: Offer }) {
  return (
    <div className="relative flex h-2 items-center">
      <div className="h-px w-full bg-border" />
      <span className="absolute left-0 size-1.5 rounded-full bg-foreground" />
      {offer.segments.slice(0, -1).map((s, i) => (
        <span
          key={s.flight}
          title={s.destination}
          className="absolute size-1.5 rounded-full border border-foreground bg-background"
          style={{ left: `${((i + 1) / offer.segments.length) * 100}%` }}
        />
      ))}
      <span className="absolute right-0 size-1.5 rounded-full bg-foreground" />
    </div>
  );
}

/**
 * Renders an offer's flights as a timeline, what it costs for everyone,
 * and where to book it.
 *
 * @param props.offer - The offer.
 * @param props.passengers - How many passengers the search was for.
 * @returns The detail.
 */
function OfferDetail({
  offer: o,
  passengers,
}: {
  offer: Offer;
  passengers: number;
}) {
  return (
    <div className="grid gap-8 px-2 pt-2 pb-6 md:grid-cols-[1fr_260px]">
      <ol className="flex flex-col">
        {o.segments.map((s, i) => (
          <li key={s.flight + s.departure} className="flex flex-col">
            <div className="grid grid-cols-[3.5rem_1rem_1fr] gap-3">
              <span className="tabular font-medium text-sm">
                {localTime(s.departure)}
              </span>
              <span className="relative flex justify-center">
                <span className="z-10 mt-1.5 size-2 rounded-full border-2 border-foreground bg-background" />
                <span className="absolute top-3 bottom-0 w-px bg-border" />
              </span>
              <span className="pb-4 text-sm">
                <span className="font-medium">{s.origin}</span>
                <span className="mt-1 block text-muted-foreground text-xs">
                  {airlineName(s.marketingCarrier)}{" "}
                  <span className="font-mono">{s.flight}</span> · {s.aircraft} ·{" "}
                  <span className="capitalize">{s.cabin}</span> ({s.bookingCode}
                  ) · {formatDuration(s.durationMinutes)}
                  {s.operatingCarrier !== s.marketingCarrier &&
                    ` · operated by ${airlineName(s.operatingCarrier)}`}
                </span>
              </span>
            </div>
            <div className="grid grid-cols-[3.5rem_1rem_1fr] gap-3">
              <span className="tabular font-medium text-sm">
                {localTime(s.arrival)}
              </span>
              <span className="flex justify-center">
                <span className="mt-1.5 size-2 rounded-full bg-foreground" />
              </span>
              <span className="font-medium text-sm">{s.destination}</span>
            </div>
            {i < o.segments.length - 1 && (
              <div className="my-3 ml-[4.75rem] w-fit rounded-md bg-muted px-2.5 py-1 text-muted-foreground text-xs">
                {formatDuration(s.layoverMinutes)} connection in {s.destination}
              </div>
            )}
          </li>
        ))}
      </ol>

      <aside className="flex flex-col gap-4 rounded-xl border bg-background p-4">
        <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">Book with</dt>
          <dd className="text-right font-medium">{programName(o.program)}</dd>
          <dt className="text-muted-foreground">
            {passengers > 1 ? `Total, ${passengers} people` : "Total"}
          </dt>
          <dd className="tabular text-right font-medium">
            {formatPoints(o.price.pointsTotal)} pts
            <span className="block font-normal text-muted-foreground text-xs">
              +{" "}
              {formatMoney(
                o.price.taxesTotal.amount,
                o.price.taxesTotal.currency,
              )}
            </span>
          </dd>
          <dt className="text-muted-foreground">Seats left</dt>
          <dd className="tabular text-right">{o.seats ?? "Not stated"}</dd>
          <dt className="text-muted-foreground">Fare</dt>
          <dd className="truncate text-right font-mono text-xs">{o.fare}</dd>
        </dl>
        {o.notes && o.notes.length > 0 && (
          <ul className="flex flex-col gap-1 text-muted-foreground text-xs">
            {o.notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        )}
        {o.bookingUrl && (
          <a
            href={o.bookingUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center justify-center gap-2 rounded-lg bg-brand px-4 py-2 font-medium text-brand-foreground text-sm transition-opacity hover:opacity-90"
          >
            Book on {programName(o.program)}
            <ExternalLink className="size-3.5" />
          </a>
        )}
        <p className="flex items-start gap-1.5 text-muted-foreground text-xs">
          <span
            className={cn(
              "mt-1 size-1.5 shrink-0 rounded-full",
              o.availability === "confirmed"
                ? "bg-cheap"
                : "bg-muted-foreground",
            )}
          />
          {o.availability === "confirmed"
            ? "Live award space. Check the price with the program before you transfer points."
            : o.availability === "estimated"
              ? "Estimated price. The seat may not be bookable."
              : "Listed without award space."}
        </p>
      </aside>
    </div>
  );
}

/**
 * Lists every date from from to to.
 *
 * @param from - The first date, YYYY-MM-DD.
 * @param to - The last date, YYYY-MM-DD.
 * @returns The dates, in order.
 */
function dateRange(from: string, to: string): string[] {
  const out: string[] = [];
  const d = new Date(`${from}T12:00:00Z`);
  const end = new Date(`${to}T12:00:00Z`);
  while (d <= end && out.length < 31) {
    out.push(d.toISOString().slice(0, 10));
    d.setUTCDate(d.getUTCDate() + 1);
  }
  return out;
}
