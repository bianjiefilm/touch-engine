// 正式商家与后台的当前组织只从受控成员列表解析。
// 手填的编号、停用成员和非成员来源都不能成为当前组织。

export type MembershipScope = {
  tenant_id: string;
  display_name: string;
  role: string;
  enabled: boolean;
  source: string;
};

export type WorkspaceTenant =
  | { status: "selected"; tenantId: string; displayName: string; role: string }
  | { status: "choose"; options: MembershipScope[] }
  | { status: "none" };

function usable(scopes: MembershipScope[] | null | undefined): MembershipScope[] {
  const seen = new Set<string>();
  const out: MembershipScope[] = [];
  for (const scope of scopes ?? []) {
    const id = scope?.tenant_id?.trim() ?? "";
    if (!scope?.enabled || scope.source !== "membership" || id === "" || seen.has(id)) continue;
    seen.add(id);
    out.push({ ...scope, tenant_id: id, display_name: scope.display_name?.trim() || "当前组织" });
  }
  return out;
}

export function resolveWorkspaceTenant(
  scopes: MembershipScope[] | null | undefined,
  remembered?: string | null,
): WorkspaceTenant {
  const options = usable(scopes);
  const rememberedID = remembered?.trim() ?? "";
  const restored = options.find((item) => item.tenant_id === rememberedID);
  const picked = restored ?? (options.length === 1 ? options[0] : undefined);
  if (picked) {
    return {
      status: "selected",
      tenantId: picked.tenant_id,
      displayName: picked.display_name,
      role: picked.role,
    };
  }
  if (options.length === 0) return { status: "none" };
  return { status: "choose", options };
}
