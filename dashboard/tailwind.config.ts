import type { Config } from "tailwindcss";

// Every color resolves to a CSS variable in globals.css, so the palette has
// one source of truth and opacity modifiers (bg-accent/15) keep working.
const token = (name: string) => `rgb(var(--${name}) / <alpha-value>)`;

const config: Config = {
  darkMode: ["class"],
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}", "./lib/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: token("bg"),
        surface: {
          DEFAULT: token("surface"),
          raised: token("surface-raised"),
          hover: token("surface-hover"),
        },
        border: {
          DEFAULT: token("border"),
          strong: token("border-strong"),
        },
        fg: {
          DEFAULT: token("fg"),
          muted: token("fg-muted"),
          subtle: token("fg-subtle"),
        },
        accent: {
          DEFAULT: token("accent"),
          hover: token("accent-hover"),
          ink: token("accent-ink"),
        },
        warn: token("warn"),
        danger: token("danger"),
      },
      borderRadius: {
        xl: "16px",
        lg: "12px",
        md: "8px",
        sm: "6px",
      },
      boxShadow: {
        panel: "inset 0 1px 0 0 rgb(255 255 255 / 0.03)",
        popover: "0 16px 40px -12px rgb(0 0 0 / 0.7)",
      },
      fontFamily: {
        sans: ["var(--font-sans)", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["var(--font-mono)", "ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
    },
  },
  plugins: [require("tailwindcss-animate")],
};

export default config;
