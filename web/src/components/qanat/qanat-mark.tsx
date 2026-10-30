import { cn } from "@/lib/utils";

/** design.md §2: the mark's look is the gateway's state for this viewer. */
export type MarkState = "still" | "flowing" | "held" | "broken";

const shafts = [24, 42, 60, 78];
// The channel slopes gently toward the outlet on the right.
const y = (x: number) => 62 + (x - 8) * 0.12;

/**
 * A cross-section of a qanat: access shafts down to a sloped channel.
 * Shafts are ink (currentColor); the water — channel, outlet and droplet — is
 * the primary color (forest; sage in dark), unless a state paints it amber or
 * red (design.md §2).
 */
export function QanatMark({
  state = "still",
  className,
  title,
  water = true,
}: {
  state?: MarkState;
  className?: string;
  title?: string;
  /** false draws the whole mark in currentColor, e.g. on the deep brand panel. */
  water?: boolean;
}) {
  const label = title ?? `Qanat, ${state}`;
  return (
    <svg viewBox="0 0 100 100" role="img" aria-label={label} className={cn("size-6 shrink-0", className)}>
      <g fill="none" stroke="currentColor" strokeWidth={6} strokeLinecap="round">
        {shafts.map((x) => (
          <line key={x} x1={x} y1={14} x2={x} y2={y(x) - 4} />
        ))}
      </g>
      <g fill="none" strokeWidth={6} strokeLinecap="round" className={water ? "stroke-primary" : "stroke-current"}>
        {state === "broken" ? (
          <>
            <line x1={8} y1={y(8)} x2={46} y2={y(46)} />
            <line x1={58} y1={y(58)} x2={86} y2={y(86)} className="stroke-destructive" />
          </>
        ) : (
          <line x1={8} y1={y(8)} x2={86} y2={y(86)} />
        )}
        {state === "held" && <line x1={54} y1={y(54)} x2={66} y2={y(66)} className="stroke-warning" />}
      </g>
      {state === "broken" ? (
        <circle cx={92} cy={y(92)} r={4} fill="none" stroke="currentColor" strokeWidth={3} className="stroke-destructive" />
      ) : (
        <circle cx={92} cy={y(92)} r={5} className={water ? "fill-primary" : "fill-current"} />
      )}
      {state === "flowing" && (
        <circle r={4} className="fill-primary motion-reduce:hidden">
          <animateMotion dur="2.4s" repeatCount="indefinite" path={`M8,${y(8)} L86,${y(86)}`} />
        </circle>
      )}
    </svg>
  );
}
