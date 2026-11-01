import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Dank Memer Farm",
  description: "Dank Memer grinder dashboard",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className="dark">
      <body>{children}</body>
    </html>
  );
}
