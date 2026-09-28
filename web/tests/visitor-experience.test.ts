import { describe, expect, it } from "vitest";
import {
  actionClickRecord,
  activityBlocks,
  beaconChannel,
  isOfficialActionUrl,
  leadDisclosureReady,
  leadOutcomeCopy,
  nfcCoverageClaim,
  participationEffects,
  presentGuestActions,
  publicSectionOrder,
} from "../src/lib/visitor-experience";

describe("公共页先讲门店价值，留资靠后", () => {
  it("区块顺序是门店、价值、动作、留资、提交后", () => {
    expect(publicSectionOrder()).toEqual(["store", "value", "actions", "lead", "next"]);
  });
});

describe("碰一下、扫码、看视频不是留资，也不进 CRM", () => {
  it("曝光、碰、扫、看视频、点按钮都不记成留资或 CRM 接收", () => {
    for (const kind of ["exposure", "nfc_touch", "qr_scan", "video_watch", "action_click"] as const) {
      expect(participationEffects({ kind })).toEqual({
        lead: false,
        crm: false,
        platformAccount: false,
        order: false,
      });
    }
  });

  it("同意后的提交只算本地留资，CRM 接收是另一件事", () => {
    expect(participationEffects({ kind: "lead_submit", consent: true })).toEqual({
      lead: true,
      crm: false,
      platformAccount: false,
      order: false,
    });
    expect(participationEffects({ kind: "lead_submit", consent: false })).toEqual({
      lead: false,
      crm: false,
      platformAccount: false,
      order: false,
    });
    expect(participationEffects({ kind: "crm_received" })).toEqual({
      lead: false,
      crm: true,
      platformAccount: false,
      order: false,
    });
  });
});

describe("拒绝营销不挡住看活动", () => {
  it("没勾营销、也没同意留资时，只能挡住提交，不能挡住浏览", () => {
    expect(activityBlocks({ marketingOptIn: false, consent: false })).toEqual(["lead"]);
  });

  it("同意告知但拒绝营销时，浏览和提交都不被营销许可挡住", () => {
    expect(activityBlocks({ marketingOptIn: false, consent: true })).toEqual([]);
  });
});

describe("留资前必须说清接收方、用途、必要字段和告知版本", () => {
  it("缺商家、用途、字段或版本时不能展示成可提交", () => {
    expect(leadDisclosureReady({
      merchant: "商家A",
      purpose: "本次活动的服务与咨询",
      requiredFields: ["name", "phone"],
      noticeVersion: "v1",
    })).toBe(true);
    expect(leadDisclosureReady({
      merchant: "",
      purpose: "本次活动的服务与咨询",
      requiredFields: ["name", "phone"],
      noticeVersion: "v1",
    })).toBe(false);
    expect(leadDisclosureReady({
      merchant: "商家A",
      purpose: "",
      requiredFields: ["name", "phone"],
      noticeVersion: "v1",
    })).toBe(false);
    expect(leadDisclosureReady({
      merchant: "商家A",
      purpose: "本次活动的服务与咨询",
      requiredFields: [],
      noticeVersion: "v1",
    })).toBe(false);
    expect(leadDisclosureReady({
      merchant: "商家A",
      purpose: "本次活动的服务与咨询",
      requiredFields: ["name", "phone"],
      noticeVersion: "",
    })).toBe(false);
  });
});

describe("提交文案不把本地接受说成销售已收到", () => {
  const merchant = "商家A";

  it("刚接受、待同步、重复提交都不说销售已收到或负责人已跟进", () => {
    for (const state of ["accepted", "pending_sync", "duplicate"]) {
      const text = leadOutcomeCopy({ merchant, state, duplicate: state === "duplicate" });
      expect(text).toContain("商家A");
      expect(text).not.toContain("销售已收到");
      expect(text).not.toContain("负责人已跟进");
    }
  });

  it("CRM 已接收仍然不是负责人已跟进", () => {
    const text = leadOutcomeCopy({ merchant, state: "crm_received", crmReceived: true });
    expect(text).toContain("客户系统已接收");
    expect(text).not.toContain("销售已收到");
    expect(text).not.toContain("负责人已跟进");
  });

  it("CRM 暂停保持暂停，不改口成已收到", () => {
    const text = leadOutcomeCopy({ merchant, state: "crm_paused" });
    expect(text).toContain("暂停");
    expect(text).not.toContain("销售已收到");
  });
});

describe("企微关注导航点评只展示真实地址，点击不是成功", () => {
  it(".invalid、本机和未证实的能力不出现", () => {
    expect(presentGuestActions([
      { kind: "wecom", available: true, result: "ready", href: "https://work.weixin.qq.com/ca/demo" },
      { kind: "follow", available: true, result: "unknown", href: "https://example.invalid/follow" },
      { kind: "navigate", available: false, result: "ready", href: "https://maps.example.com/pin" },
      { kind: "review", available: true, result: "ready", href: "http://127.0.0.1:18340/review" },
    ])).toEqual([
      { kind: "wecom", href: "https://work.weixin.qq.com/ca/demo" },
    ]);
  });

  it("点击只记成未知结果，不是添加成功", () => {
    expect(actionClickRecord("wecom")).toEqual({
      kind: "wecom",
      recordedAs: "click",
      success: false,
      platformResult: "unknown",
    });
  });

  it("正式目标拒绝 .invalid 和临时本机地址", () => {
    expect(isOfficialActionUrl("https://example.invalid/go")).toBe(false);
    expect(isOfficialActionUrl("https://localhost/go")).toBe(false);
    expect(isOfficialActionUrl("https://127.0.0.1/go")).toBe(false);
    expect(isOfficialActionUrl("https://app.local/go")).toBe(false);
    expect(isOfficialActionUrl("https://192.168.1.8/go")).toBe(false);
    expect(isOfficialActionUrl("https://work.weixin.qq.com/ca/demo")).toBe(true);
  });
});

describe("来源只做统计，不改租户", () => {
  it("nfc 和 qr 记成来源，查询里的租户被丢掉", () => {
    expect(beaconChannel("nfc", "tnt_evil")).toEqual({ channel: "nfc", tenantOverride: null });
    expect(beaconChannel("qr", "tnt_evil")).toEqual({ channel: "qr", tenantOverride: null });
    expect(beaconChannel("web", null)).toEqual({ channel: "web", tenantOverride: null });
    expect(beaconChannel("miniprogram", "tnt_evil")).toEqual({ channel: "web", tenantOverride: null });
  });
});

describe("只测了链接不能宣称全部机型都能碰", () => {
  it("没有实机清单时不宣称全机型兼容", () => {
    expect(nfcCoverageClaim({ testedUrls: true, devices: [] })).toEqual({
      allModels: false,
      devices: [],
    });
  });
});
