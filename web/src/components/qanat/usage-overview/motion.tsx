/*
 * Motion for the usage pages, from Animate UI's tokens
 * (@animate-ui/react/utils/motion). The page reveals block by block once,
 * headline numbers count up on load and spring to each new value as the
 * data refreshes. Under reduced motion blocks only fade and numbers show
 * their value at once.
 */
"use client";

import { blur, spring, transition, useShouldReduceMotion, useTempoTransition } from "@animate-ui/react/utils/motion";
import { animate, motion, useMotionValue, useTransform } from "motion/react";
import { useEffect, type ReactNode } from "react";

import { cn } from "@/lib/utils";

/** Reveal fades a block up into place once, `index` steps (60 ms) after the first. */
export function Reveal({ index = 0, className, children }: { index?: number; className?: string; children: ReactNode }) {
  const reduce = useShouldReduceMotion();
  const rise = useTempoTransition(spring.entrance);
  const fade = useTempoTransition(transition.reveal);
  const delay = Math.min(index, 8) * 0.06;
  return (
    <motion.div
      className={className}
      initial={{
        opacity: 0,
        y: reduce ? 0 : 10,
        filter: blur.surface,
      }}
      animate={{
        opacity: 1,
        y: 0, // motion drops the transform at rest, so nothing below gets a new containing block
        filter: "blur(0px)",
        transition: {
          default: { ...rise, delay },
          opacity: { ...fade, delay },
          filter: { ...fade, delay },
        },
      }}
    >
      {children}
    </motion.div>
  );
}

/**
 * CountUp counts from zero to `value` on mount, then springs to each new
 * value. Screen readers get the final text once, not every frame.
 */
export function CountUp({ value, format, className }: { value: number; format: (n: number) => string; className?: string }) {
  const reduce = useShouldReduceMotion();
  const settle = useTempoTransition(spring.number);
  const mv = useMotionValue(reduce ? value : 0);
  const text = useTransform(mv, format);

  useEffect(() => {
    if (reduce) {
      mv.set(value);
      return;
    }
    const controls = animate(mv, value, settle);
    return () => controls.stop();
  }, [value, reduce, mv, settle]);

  return (
    <span className={cn("tabular-nums", className)}>
      <span className="sr-only">{format(value)}</span>
      <motion.span aria-hidden>{text}</motion.span>
    </span>
  );
}
