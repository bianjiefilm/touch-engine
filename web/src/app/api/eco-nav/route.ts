import { NextResponse } from "next/server";
import { ecoNavEndpoint, ecoNavIdentityEnabled } from "@/lib/eco-nav/env";
import { loadEcoNavModel } from "@/lib/eco-nav/load";

export async function GET() {
  const model = await loadEcoNavModel({
    endpoint: ecoNavEndpoint(),
    identityEnabled: ecoNavIdentityEnabled(),
  });
  return NextResponse.json(model);
}
