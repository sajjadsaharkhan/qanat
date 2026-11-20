import { describe, expect, it } from "vitest";
import { msHeader, readEvent, takeEvents, timeline } from "./playground";

describe("takeEvents", () => {
  it("returns complete events and keeps the unfinished tail", () => {
    const { events, rest } = takeEvents('event: a\ndata: {"type":"ping"}\n\nevent: b\ndata: {"type":"content_blo');
    expect(events).toEqual([{ type: "ping" }]);
    expect(rest).toBe('event: b\ndata: {"type":"content_blo');
  });
  it("skips payloads that aren't JSON", () => {
    expect(takeEvents(": keep-alive\n\ndata: nope\n\n").events).toEqual([]);
  });
});

describe("readEvent", () => {
  it("reads text, tokens and errors", () => {
    expect(readEvent({ type: "content_block_delta", delta: { type: "text_delta", text: "hi" } }).text).toBe("hi");
    expect(readEvent({ type: "message_start", message: { usage: { input_tokens: 12, output_tokens: 1 } } })).toMatchObject({ inTokens: 12, outTokens: 1 });
    expect(readEvent({ type: "message_delta", usage: { output_tokens: 40 } }).outTokens).toBe(40);
    expect(readEvent({ type: "error", error: { message: "busy" } }).error).toBe("busy");
  });
});

describe("msHeader and timeline", () => {
  it("parses numbers and ignores absent headers", () => {
    const h = new Headers({ "x-qanat-gateway-ms": "1.5" });
    expect(msHeader(h, "x-qanat-gateway-ms")).toBe(1.5);
    expect(msHeader(h, "x-qanat-connect-ms")).toBeUndefined();
  });
  it("splits a run into legs that add up to the total", () => {
    const t = timeline({ gateway: 5, upstream: 300, ttft: 420, total: 1500 });
    expect(t).toEqual({ gateway: 5, upstream: 300, toFirst: 115, generation: 1080 });
    expect(timeline({ gateway: 5 })).toBeNull();
  });
});
