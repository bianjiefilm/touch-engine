import type { Metadata } from "next";
import type { ReactNode } from "react";

export const metadata: Metadata = {
  title: "门店活动",
  description: "看这家店这次活动可以做什么",
};

export default function PublicCampaignLayout({ children }: { children: ReactNode }) {
  return <div data-pn-surface="public.consumer" data-pn-theme="light">{children}</div>;
}
