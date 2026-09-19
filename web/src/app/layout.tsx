import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "碰一碰 · 商家线下活动",
  description: "touch-engine:碰一碰独立应用(商家线下活动/内容分发)",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body>{children}</body>
    </html>
  );
}
