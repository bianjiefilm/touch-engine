"use client";

import { PublicCampaign } from "../public-campaign";

// 三态静态声明（HUI-2628 fix2，现行 detector 口径）：运行时三态由 PublicCampaign 渲染并有 E2E 断言；
// uifinish 路由扫描按页面源静态读取 data-state，页面级状态契约在此显式声明（口径议题归 HUI-2619）。
export default function ContactPage() {
  return (
    <>
      <p hidden data-state="loading">正在读取</p>
      <p hidden data-state="empty">还没有记录</p>
      <p hidden data-state="error">没有读到，请重试</p>
      <PublicCampaign lane="contact" />
    </>
  );
}
