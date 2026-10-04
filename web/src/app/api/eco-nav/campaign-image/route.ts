import { NextResponse } from "next/server";
import { acceptCampaignImage, type CampaignImageEnv } from "@/lib/eco-nav/campaign-image";

export async function POST(request: Request) {
  const body = (await request.json().catch(() => null)) as { campaign_id?: string } | null;
  const campaignId = body?.campaign_id?.trim() ?? "";
  if (!campaignId) {
    return NextResponse.json({ message: "缺少活动编号" }, { status: 400 });
  }
  try {
    // HUI-2628 r2 基线修复：ProcessEnv（含索引签名）与全可选的 CampaignImageEnv 触发弱类型检查报错。
    // 交叉类型断言只做类型层面的适配，不改运行时行为。
    const result = await acceptCampaignImage(campaignId, process.env as NodeJS.ProcessEnv & CampaignImageEnv);
    return NextResponse.json(result);
  } catch (error) {
    const message = error instanceof Error ? error.message : "产品图没有创建或恢复工程";
    return NextResponse.json({ message, charges_customer: false }, { status: 409 });
  }
}
