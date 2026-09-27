import { NextResponse } from "next/server";
import { acceptCampaignImage } from "@/lib/eco-nav/campaign-image";

export async function POST(request: Request) {
  const body = (await request.json().catch(() => null)) as { campaign_id?: string } | null;
  const campaignId = body?.campaign_id?.trim() ?? "";
  if (!campaignId) {
    return NextResponse.json({ message: "缺少活动编号" }, { status: 400 });
  }
  try {
    const result = await acceptCampaignImage(campaignId, process.env);
    return NextResponse.json(result);
  } catch (error) {
    const message = error instanceof Error ? error.message : "产品图没有创建或恢复工程";
    return NextResponse.json({ message, charges_customer: false }, { status: 409 });
  }
}
