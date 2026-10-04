import type { Metadata } from "next";
import "./globals.css";
import "./touch.css";
import "../styles/generated/painuo/v1/tokens.css";
import "../styles/generated/painuo/v1/aliases.css";

export const metadata: Metadata = {
  title: "碰一碰",
  description: "看今天门店要处理的活动",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN">
      <body data-pn-surface="work.light" data-pn-theme="light">{children}</body>
    </html>
  );
}
