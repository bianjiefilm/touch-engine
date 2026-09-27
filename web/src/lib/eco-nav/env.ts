export function ecoNavEndpoint(): string | null {
  const raw = process.env.PUBLIC_AI_ECO_NAV_URL?.trim() ?? "";
  return raw === "" ? null : raw;
}

export function ecoNavIdentityEnabled(): boolean {
  return process.env.PUBLIC_AI_ECO_NAV_IDENTITY === "1";
}
