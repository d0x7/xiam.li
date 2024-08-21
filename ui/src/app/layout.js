import "./globals.css";

import { Analytics } from "@vercel/analytics/react"
import { SpeedInsights } from "@vercel/speed-insights/next"
import { Toaster } from "@/components/ui/sonner"
import { sharedLongDesc, sharedTitle } from "@/lib/constants";

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body className="dark h-screen">
        {children}
        <Toaster />
        <Analytics />
        <SpeedInsights />
      </body>
    </html>
  );
}


export const metadata = {
  metadataBase: new URL('https://xiam.li'),
  title: sharedTitle,
  description: sharedLongDesc,
  robots: {
    index: true,
    follow: true
  },
  openGraph: {
    title: sharedTitle,
    description: sharedLongDesc,
    alt: sharedTitle,
    type: 'website',
    url: '/',
    siteName: sharedTitle,
    locale: 'en_IE',
    // image: ...
  },
  alternates: {
    canonical: '/'
  },
  other: {
    pinterest: 'nopin'
  }
  }
