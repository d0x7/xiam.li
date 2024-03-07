import "./globals.css";

import { Toaster } from "@/components/ui/sonner"

export const metadata = {
  title: "go.sazak.io - Ozan Sazak",
  description: "Go Package Index of Ozan Sazak",
};

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body className="dark h-screen">
        {children}
        <Toaster />
      </body>
    </html>
  );
}
