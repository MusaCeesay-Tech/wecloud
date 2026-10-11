import "./globals.css";

export const metadata = {
  title: "WeCloud — the peer-to-peer cloud",
  description:
    "Deploy web apps to a global mesh of volunteer machines. Give compute, take credits. Free, open, peer-to-peer.",
};

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
