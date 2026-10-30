/*
 * Subtle motion for the Network page, from Animate UI's tokens
 * (@animate-ui/react/utils/motion): blocks reveal once on first load, numbers
 * roll when they change, a status label swaps with a light blur, surfaces
 * press. Under reduced motion things still fade, nothing moves.
 */
"use client";

import { SlidingNumber } from "@animate-ui/react/primitives/texts/sliding-number";
import { blur, transition, useMotionTokens, useShouldReduceMotion, useTempoTransition } from "@animate-ui/react/utils/motion";
import { AnimatePresence, motion } from "motion/react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Reveal fades a block up into place once, `index` steps after the first. */
export function Reveal({ index = 0, className, children }: { index?: number; className?: string; children: ReactNode }) {
  const reduce = useShouldReduceMotion();
  const { spring } = useMotionTokens();
  const fade = useTempoTransition(transition.reveal);
  const delay = index * 0.08; // 80 ms between blocks (recipes/page.md)
  return (
    <motion.div
      className={className}
      initial={{ opacity: 0, transform: reduce ? "none" : "translateY(12px)", filter: blur.surface }}
      animate={{
        opacity: 1,
        transform: reduce ? "none" : "translateY(0px)",
        filter: "blur(0px)",
        transition: { default: { ...spring.entrance, delay }, opacity: { ...fade, delay }, filter: { ...fade, delay } },
      }}
    >
      {children}
    </motion.div>
  );
}

/** Rolling renders a number that rolls to its new value; the value is read once by screen readers. */
export function Rolling({ value, decimals = 0, className }: { value: number; decimals?: number; className?: string }) {
  const { spring } = useMotionTokens();
  const rounded = Number(value.toFixed(decimals));
  return (
    <span className={cn("inline-flex tabular-nums", className)} aria-label={rounded.toFixed(decimals)}>
      <SlidingNumber number={rounded} decimalPlaces={decimals} initiallyStable transition={spring.number} aria-hidden />
    </span>
  );
}

/** Swap cross-fades content in place when `id` changes (a status that moved). */
export function Swap({ id, children, className }: { id: string; children: ReactNode; className?: string }) {
  const swap = useTempoTransition(transition.swap);
  return (
    <span className={cn("relative inline-flex", className)}>
      <AnimatePresence mode="popLayout" initial={false}>
        <motion.span
          key={id}
          className="inline-flex"
          initial={{ opacity: 0, filter: blur.icon }}
          animate={{ opacity: 1, filter: "blur(0px)", transition: swap }}
          exit={{ opacity: 0, filter: blur.icon, transition: swap }}
        >
          {children}
        </motion.span>
      </AnimatePresence>
    </span>
  );
}

/** usePress gives a surface its press: 0.97 for buttons, 0.985 for cards. */
export function usePress(kind: "button" | "card") {
  const reduce = useShouldReduceMotion();
  const { spring, scale } = useMotionTokens();
  const to = reduce ? 1 : kind === "card" ? scale.choice : scale.press;
  return {
    initial: { transform: "scale(1)" },
    whileTap: { transform: `scale(${to})` },
    transition: spring.press,
  } as const;
}
