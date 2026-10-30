// Shared test setup. jest-dom matchers are registered only where a DOM exists,
// so node-environment tests stay dependency-light.
if (typeof window !== "undefined") {
  await import("@testing-library/jest-dom/vitest");
}

export {};
