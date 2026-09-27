import { NextResponse } from "next/server";
import { registeredLaunchMap } from "@/lib/eco-nav/allowlist";

export async function GET() {
  return NextResponse.json(registeredLaunchMap());
}
