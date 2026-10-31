/**
 * Enforces design.md §3 and §10.3 rule 3: every text/surface pairing meets
 * WCAG 2.1 AA (4.5:1), and every signal fill meets 3:1 against the page and
 * the quota-bar track, in both themes. Reads the real globals.css, so a token
 * change that breaks contrast fails CI.
 */
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const css = readFileSync(resolve(__dirname, "../app/globals.css"), "utf8");

function block(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`selector ${selector} not found`);
  const body = css.slice(start, css.indexOf("}", start));
  const vars: Record<string, string> = {};
  for (const [, name, value] of body.matchAll(/--([\w-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    if (name && value) vars[name] = value;
  }
  return vars;
}

const light = block(":root");
const dark = { ...light, ...block(".dark") }; // .dark overrides :root

function token(theme: Record<string, string>, name: string): string {
  const v = theme[name];
  if (!v) throw new Error(`token --${name} missing from globals.css`);
  return v;
}

function channel(hex: string, i: number): number {
  const x = parseInt(hex.slice(i, i + 2), 16) / 255;
  return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
}

function luminance(hex: string): number {
  return 0.2126 * channel(hex, 1) + 0.7152 * channel(hex, 3) + 0.0722 * channel(hex, 5);
}

function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

const surfaces = ["background", "card", "muted", "accent", "sidebar", "brand-soft", "sand-soft"];
const texts = ["foreground", "muted-foreground", "warning-text", "destructive-text", "success-text", "brand-text", "sand-text"];
const fills = ["warning", "destructive", "success", "primary"];
// Switches sit on any surface; the thumb must read on both track states.
const switchSurfaces = ["background", "card", "muted", "sidebar"];

for (const [theme, t] of [
  ["light", light],
  ["dark", dark],
] as const) {
  describe(`${theme} theme`, () => {
    for (const text of texts)
      for (const surface of surfaces)
        it(`${text} on ${surface} ≥ 4.5:1`, () => {
          expect(contrast(token(t, text), token(t, surface))).toBeGreaterThanOrEqual(4.5);
        });

    it("button text on primary ≥ 4.5:1", () => {
      expect(contrast(token(t, "primary-foreground"), token(t, "primary"))).toBeGreaterThanOrEqual(4.5);
    });

    it("active nav item text ≥ 4.5:1", () => {
      expect(contrast(token(t, "sidebar-accent-foreground"), token(t, "sidebar-accent"))).toBeGreaterThanOrEqual(4.5);
      expect(contrast(token(t, "sidebar-foreground"), token(t, "sidebar-accent"))).toBeGreaterThanOrEqual(4.5);
    });

    it("success text on its soft fill ≥ 4.5:1", () => {
      expect(contrast(token(t, "success-text"), token(t, "success-soft"))).toBeGreaterThanOrEqual(4.5);
    });

    it("text on sand (avatar) ≥ 4.5:1", () => {
      expect(contrast(token(t, "sand-foreground"), token(t, "sand"))).toBeGreaterThanOrEqual(4.5);
    });

    it("text on the deep brand panel ≥ 4.5:1", () => {
      expect(contrast(token(t, "brand-deep-foreground"), token(t, "brand-deep"))).toBeGreaterThanOrEqual(4.5);
    });

    for (const fill of fills)
      for (const against of ["background", "muted"])
        it(`${fill} fill vs ${against} ≥ 3:1`, () => {
          expect(contrast(token(t, fill), token(t, against))).toBeGreaterThanOrEqual(3);
        });

    for (const track of ["switch-on", "switch-off"]) {
      it(`switch thumb on ${track} ≥ 3:1`, () => {
        expect(contrast(token(t, "switch-thumb"), token(t, track))).toBeGreaterThanOrEqual(3);
      });
      for (const surface of switchSurfaces)
        it(`${track} track vs ${surface} ≥ 3:1`, () => {
          expect(contrast(token(t, track), token(t, surface))).toBeGreaterThanOrEqual(3);
        });
    }
  });
}
