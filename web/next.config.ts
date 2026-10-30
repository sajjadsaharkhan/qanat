import type { NextConfig } from "next";

const config: NextConfig = {
  // Self-contained server for the Docker image (deploy/local).
  output: "standalone",
  poweredByHeader: false,
  // forbidden() for admin pages, so a regular user gets a real 403.
  experimental: { authInterrupts: true },
};

export default config;
