import { describe, expect, it } from "vitest";
import {
  authorizedReturnHref,
  canonicalHref,
  capabilityGapLines,
  closedNotices,
  honestOpen,
  merchantJumpKinds,
  openMode,
  putJumpActions,
  openedEvidenceCopy,
  saveJumpError,
  standingEvidenceCopy,
} from "../src/lib/jump-matrix";

describe("jump matrix visitor policy", () => {
  it("服务端地址和按钮不一致时不打开", () => {
    const server = {
      href: "https://shop.example.com/wifi",
      recorded_as: "click",
      success: false,
      platform_result: "unknown",
    };
    expect(honestOpen("https://evil.example/phish", server)).toBe(false);
    expect(canonicalHref("https://evil.example/phish", server.href)).toBe("");
    expect(honestOpen("https://shop.example.com/wifi", server)).toBe(true);
    expect(canonicalHref("https://shop.example.com/wifi", server.href)).toBe("https://shop.example.com/wifi");
  });

  it("服务端声称成功时仍然不打开", () => {
    expect(honestOpen("https://shop.example.com/wifi", {
      href: "https://shop.example.com/wifi",
      recorded_as: "success",
      success: true,
      platform_result: "followed",
    })).toBe(false);
    expect(honestOpen("https://shop.example.com/follow", null)).toBe(false);
  });

  it("关闭原因只保留失效、撤销、未授权和跨品牌，并且丢掉带回地址的行", () => {
    const notices = closedNotices([
      { kind: "navigate", reason: "expired" },
      { kind: "review", reason: "revoked" },
      { kind: "wifi", reason: "unauthorized" },
      { kind: "follow", reason: "cross_brand" },
      { kind: "wecom", reason: "expired" },
      { kind: "navigate", reason: "not_configured" },
      { kind: "wifi", reason: "address_not_official", href: "http://127.0.0.1/wifi" },
      { kind: "review", reason: "revoked", href: "https://shop.example.com/review" },
    ]);
    expect(notices.map((item) => `${item.kind}:${item.reason}`)).toEqual([
      "navigate:expired",
      "review:revoked",
      "wifi:unauthorized",
      "follow:cross_brand",
    ]);
    const text = JSON.stringify(notices);
    expect(text).not.toContain("shop.example.com");
    expect(text).not.toContain("127.0.0.1");
    expect(text).not.toContain("href");
    expect(notices[0].text).toContain("失效");
    expect(notices[1].text).toContain("撤销");
    expect(notices[2].text).toContain("未授权");
    expect(notices[3].text).toContain("跨品牌");
  });

  it("缺口文案分开三种证据，并写明原生唤起未验证", () => {
    const gaps = capabilityGapLines().join("\n");
    expect(gaps).toContain("原生唤起未验证");
    expect(gaps).not.toContain("未安装");
    expect(gaps).not.toMatch(/已关注|已加群|已支付|添加成功|关注成功|点评成功/);
    const standing = standingEvidenceCopy();
    expect(standing).toContain("网页可达");
    expect(standing).toContain("原生唤起未验证");
    expect(standing).toContain("未知");
    const opened = openedEvidenceCopy();
    expect(opened).toBe("网页已打开，这只说明网页可达。原生唤起未验证。平台动作仍是未知，不是加企微、关注或点评成功。");
    expect(standing).toBe("网页地址只说明网页可达。原生唤起未验证。平台动作仍是未知，不是加企微、关注或点评成功。");
    expect(gaps).toContain("不会自动关注");
    expect(gaps).toContain("自动加群");
    expect(gaps).toContain("自动付费");
  });

  it("授权回跳只在服务端标记展示时给出地址，站内用当前页跳转", () => {
    expect(authorizedReturnHref({ shown: true, href: "/c/welcome" })).toBe("/c/welcome");
    expect(authorizedReturnHref({ shown: true, href: "https://h5.example.com/c/back" })).toBe("https://h5.example.com/c/back");
    expect(authorizedReturnHref({ shown: false, href: "https://evil.example/back" })).toBe("");
    expect(authorizedReturnHref({ shown: true, href: "" })).toBe("");
    expect(authorizedReturnHref(null)).toBe("");
    expect(openMode("/c/welcome")).toBe("assign");
    expect(openMode("https://h5.example.com/c/back")).toBe("blank");
    expect(openMode("//evil.example")).toBe("refuse");
    expect(openMode("javascript:alert(1)")).toBe("refuse");
  });

  it("商家配置不含加企微，未登记地址有固定拒绝文案", () => {
    expect(merchantJumpKinds()).toEqual(["wifi", "navigate", "review", "follow"]);
    expect(saveJumpError("unregistered_url")).toBe("未登记的外部地址已拒绝");
  });

  it("保存店内跳转时保留已有企微配置，但不把它放进可编辑种类", () => {
    const actions = putJumpActions(
      {
        wifi: { enabled: true, href: "https://shop.example.com/wifi", revoked: false, expires_at: "" },
        navigate: { enabled: false, href: "", revoked: false, expires_at: "" },
        review: { enabled: false, href: "", revoked: false, expires_at: "" },
        follow: { enabled: true, href: "https://shop.example.com/follow", revoked: false, expires_at: "" },
      },
      [{ kind: "wecom", enabled: true, href: "https://work.weixin.qq.com/ca/from-jump", revoked: false, expires_at: "" }],
    );
    expect(actions.map((item) => item.kind)).toEqual(["wifi", "navigate", "review", "follow", "wecom"]);
    expect(actions.find((item) => item.kind === "wecom")?.href).toBe("https://work.weixin.qq.com/ca/from-jump");
  });
});
