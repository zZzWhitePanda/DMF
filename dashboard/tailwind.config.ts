import type { Config } from "tailwindcss";

const config: Config = {
  darkMode: ["class"],
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}", "./lib/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // Memeforge palette — matches the TUI's accent/success/warn/error.
        accent: {
          DEFAULT: "#7F5AF0",
          foreground: "#FFFFFE",
        },
        success: "#2CB67D",
        warn: "#FF8906",
        error: "#EF4565",
        panel: "#242629",
        muted: "#72757E",
        background: "#0F0F11",
        foreground: "#FFFFFE",
        border: "#2C2E33",
        card: "#1A1A1D",
      },
      borderRadius: {
        lg: "0.75rem",
        md: "0.5rem",
        sm: "0.375rem",
      },
      fontFamily: {
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "Monaco", "Consolas", "monospace"],
      },
    },
  },
  plugins: [require("tailwindcss-animate")],
};

export default config;
