import { describe, expect, it } from "vitest";
import {
  actionEnabled,
  authorizedPublishEnabled,
  closedEvidenceLinks,
  draftMatchesAttempt,
  outcomeMessage,
  publishSucceeded,
  type PlatformRow,
} from "../src/lib/customer-publish";

const douyin: PlatformRow = {
  platform: "douyin",
  manual_guide: "请顾客手动打开抖音",
  capabilities: {
    preview: { enabled: true, reason: "本地预览" },
    export: { enabled: true, reason: "合规导出" },
    open_editor: { enabled: false, reason: "没有已验证应用", evidence_url: "https://developer.open-douyin.com" },
    authorized_publish: { enabled: false, reason: "未授权外发", evidence_url: "https://developer.open-douyin.com" },
    confirm_publish: { enabled: false, reason: "没有官方回执", evidence_url: "https://developer.open-douyin.com" },
  },
};

describe("发布成功只认可核实回执", () => {
  it("预览、导出、确认、自报和 unknown 都不是发布成功", () => {
    for (const body of [
      { status: "previewed", counts_as_published: false, publish_success: false },
      { status: "exported", counts_as_published: false, publish_success: false },
      { status: "editor_opened", counts_as_published: false, publish_success: false },
      { status: "publish_requested", counts_as_published: false, publish_success: false },
      { status: "unknown", counts_as_published: false, publish_success: false },
      { status: "exported", self_reported: true, counts_as_published: false, publish_success: false },
      { status: "publish_confirmed", counts_as_published: false, publish_success: true },
      { status: "exported", counts_as_published: true, publish_success: true },
    ]) {
      expect(publishSucceeded(body)).toBe(false);
    }
  });

  it("只有确认状态且计数和成功标记同时为真才算发布成功", () => {
    expect(publishSucceeded({
      status: "publish_confirmed",
      counts_as_published: true,
      publish_success: true,
    })).toBe(true);
  });

  it("自报文案明确不是成功", () => {
    expect(outcomeMessage({ status: "exported", self_reported: true, publish_success: false })).toContain("自报");
    expect(outcomeMessage({ status: "unknown", publish_success: false })).toContain("未知");
  });
});

describe("登记适配器不会打开授权发布", () => {
  it("四个关闭项保持关闭", () => {
    expect(actionEnabled(douyin, "preview")).toBe(true);
    expect(actionEnabled(douyin, "export")).toBe(true);
    expect(actionEnabled(douyin, "open_editor")).toBe(false);
    expect(actionEnabled(douyin, "authorized_publish")).toBe(false);
    expect(actionEnabled(douyin, "confirm_publish")).toBe(false);
  });

  it("文案或账号和已保存的准备不一致时不能确认", () => {
    const saved = { copy: "原文案", account_label: "顾客抖音" };
    expect(draftMatchesAttempt(saved, "原文案", "顾客抖音")).toBe(true);
    expect(draftMatchesAttempt(saved, "新文案", "顾客抖音")).toBe(false);
    expect(draftMatchesAttempt(saved, "原文案", "另一个账号")).toBe(false);
    expect(draftMatchesAttempt(null, "原文案", "顾客抖音")).toBe(false);
  });

  it("关闭的能力带上官方文档地址", () => {
    const links = closedEvidenceLinks(douyin);
    expect(links.map((item) => item.kind).sort()).toEqual(["authorized_publish", "confirm_publish", "open_editor"]);
    expect(links.every((item) => item.url.startsWith("https://"))).toBe(true);
  });

  it("registered_adapters 非空仍然不能授权发布", () => {
    expect(authorizedPublishEnabled({
      adapters_do_not_enable: true,
      registered_adapters: ["douyin-openapi", "kuaishou-openapi"],
      platforms: [douyin],
    })).toBe(false);
  });
});
