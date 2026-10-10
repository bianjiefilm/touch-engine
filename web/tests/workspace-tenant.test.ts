import { describe, expect, it } from "vitest";
import { resolveWorkspaceTenant, type MembershipScope } from "@/lib/workspace-tenant";

function scope(partial: Partial<MembershipScope> & Pick<MembershipScope, "tenant_id" | "display_name">): MembershipScope {
  return {
    role: "org_owner",
    enabled: true,
    source: "membership",
    ...partial,
  };
}

describe("resolveWorkspaceTenant", () => {
  it("唯一受控成员组织直接进入，不要求手填编号", () => {
    const got = resolveWorkspaceTenant([scope({ tenant_id: "tnt_a", display_name: "商家甲" })], null);
    expect(got).toEqual({ status: "selected", tenantId: "tnt_a", displayName: "商家甲", role: "org_owner" });
  });

  it("重登只恢复仍在成员列表里的组织", () => {
    const scopes = [
      scope({ tenant_id: "tnt_a", display_name: "商家甲" }),
      scope({ tenant_id: "tnt_b", display_name: "商家乙", role: "staff" }),
    ];
    expect(resolveWorkspaceTenant(scopes, "tnt_b").status).toBe("selected");
    if (resolveWorkspaceTenant(scopes, "tnt_b").status === "selected") {
      expect(resolveWorkspaceTenant(scopes, "tnt_b")).toMatchObject({ tenantId: "tnt_b", displayName: "商家乙" });
    }
  });

  it("多个组织且没有合法记忆时只给名称选择，不接受手填的跨租户编号", () => {
    const scopes = [
      scope({ tenant_id: "tnt_a", display_name: "商家甲" }),
      scope({ tenant_id: "tnt_b", display_name: "商家乙" }),
    ];
    const got = resolveWorkspaceTenant(scopes, "tnt_foreign");
    expect(got.status).toBe("choose");
    if (got.status === "choose") {
      expect(got.options.map((item) => item.display_name)).toEqual(["商家甲", "商家乙"]);
      expect(got.options.some((item) => item.tenant_id === "tnt_foreign")).toBe(false);
    }
  });

  it("停用、空编号和非成员来源不能成为当前组织", () => {
    expect(resolveWorkspaceTenant([
      scope({ tenant_id: "tnt_off", display_name: "停用", enabled: false }),
      scope({ tenant_id: "  ", display_name: "空" }),
      scope({ tenant_id: "tnt_typed", display_name: "手填", source: "typed" }),
    ], "tnt_typed")).toEqual({ status: "none" });
  });
});
