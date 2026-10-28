import type { NextConfig } from "next";

const config: NextConfig = {
  // A self-contained server in .next/standalone, for a small Docker image.
  output: "standalone",
  poweredByHeader: false,
};

export default config;
