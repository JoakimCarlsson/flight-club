import { useState } from "react";
import { airlineName } from "@/lib/offers";
import { cn } from "@/lib/utils";

/**
 * Renders an airline's tail logo, falling back to its code when the image
 * cannot load.
 *
 * @param props.code - The airline's IATA code.
 * @param props.className - Extra classes for the frame.
 * @returns The logo.
 */
export function AirlineLogo({
  code,
  className,
}: {
  code: string;
  className?: string;
}) {
  const [failed, setFailed] = useState(false);
  return (
    <span
      title={airlineName(code)}
      className={cn(
        "flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-lg border bg-white",
        className,
      )}
    >
      {failed ? (
        <span className="font-mono font-semibold text-[11px] text-neutral-700">
          {code}
        </span>
      ) : (
        <img
          src={`https://images.kiwi.com/airlines/64/${code}.png`}
          alt={airlineName(code)}
          className="size-7 object-contain"
          onError={() => setFailed(true)}
        />
      )}
    </span>
  );
}
