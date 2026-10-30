import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

// design.md §10.3: tokens only, RTL-safe spacing.
const paletteClass =
  /\b(?:bg|text|border|ring|fill|stroke|from|to|via|outline|divide|shadow)-(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3}\b/;
const physicalSpacing = /(?:^|\s)-?(?:ml|mr|pl|pr|left|right)-[\w[\]./-]+/;

const config = [
  ...nextVitals,
  ...nextTs,
  { ignores: [".next/**", "playwright-report/**", "test-results/**"] },
  {
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/components/ui/**", "src/**/*.test.{ts,tsx}"],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector: "Literal[value=/#[0-9a-fA-F]{3,8}\\b/]",
          message: "Use a design token (design.md §3), not a hex color.",
        },
        {
          selector: `Literal[value=${paletteClass}]`,
          message: "Use a semantic token class (bg-muted, text-warning-text), not a Tailwind palette color.",
        },
        {
          selector: `Literal[value=${physicalSpacing}]`,
          message: "Use logical utilities (ms-, me-, ps-, pe-, start-, end-) so RTL works (design.md §10.3).",
        },
      ],
    },
  },
];

export default config;
