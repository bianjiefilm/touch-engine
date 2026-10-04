import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { STATE_ACTION, STATE_TEXT } from "../src/lib/public-state";
import {
  actionClickRecord,
  activityBlocks,
  anonymousJourney,
  guestActionLabel,
  guestActionsFromPayload,
  settleClick,
  beaconChannel,
  isOfficialActionUrl,
  leadDisclosureReady,
  leadOutcomeCopy,
  nfcCoverageClaim,
  participationEffects,
  presentGuestActions,
  publicSectionOrder,
  publicVisitorColumns,
  separateActivityCounts,
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

  it("刚接受和待同步写待同步，不说销售已收到或负责人已跟进", () => {
    for (const state of ["accepted", "pending_sync"]) {
      const text = leadOutcomeCopy({ merchant, state });
      expect(text).toContain("已提交给商家A");
      expect(text).toContain("待同步");
      expect(text).not.toContain("销售已收到");
      expect(text).not.toContain("负责人已跟进");
    }
  });

  it("重复提交仍是重复，不说销售已收到", () => {
    const text = leadOutcomeCopy({ merchant, state: "duplicate", duplicate: true });
    expect(text).toContain("商家A");
    expect(text).toContain("没有再记一条");
    expect(text).not.toContain("销售已收到");
    expect(text).not.toContain("负责人已跟进");
  });

  it("只有测试注入的确认接收数大于 0 才写销售已收到，页面默认不传这个数", () => {
    expect(leadOutcomeCopy({ merchant, state: "accepted", confirmedReceiptCount: 0 })).toContain("待同步");
    expect(leadOutcomeCopy({ merchant, state: "accepted" })).not.toContain("销售已收到");
    expect(leadOutcomeCopy({ merchant, state: "accepted", confirmedReceiptCount: 2 })).toBe("销售已收到 2");
    const source = readFileSync(new URL("../src/app/c/[code]/page.tsx", import.meta.url), "utf8");
    expect(source).not.toMatch(/confirmedReceiptCount/);
    expect(source).not.toContain("销售已收到");
  });

  it("CRM 已接收仍然不是负责人已跟进", () => {
    const text = leadOutcomeCopy({
      merchant,
      state: "crm_received",
      crmReceived: true,
      salesReceived: true,
      ownerFollowedUp: true,
    });
    expect(text).toContain("客户系统已接收");
    expect(text).toContain("还不知道");
    expect(text).not.toContain("销售已收到");
    expect(text).not.toContain("负责人已跟进");
    expect(text).not.toContain("这还不是负责人跟进");
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

  it("未配置和关闭的动作不会被公共载荷变成按钮", () => {
    const visible = presentGuestActions(guestActionsFromPayload({
      actions: [
        { kind: "wifi", available: true, result: "ready", href: "https://shop.example.com/wifi" },
        { kind: "wecom", available: true, result: "ready", href: "https://example.invalid/wecom" },
        { kind: "crm", available: true, result: "ready", href: "https://crm.example.com/import" },
      ],
      closed: [
        { kind: "navigate", shown: false, reason: "not_configured" },
      ],
    } as Parameters<typeof guestActionsFromPayload>[0]));
    expect(visible).toEqual([{ kind: "wifi", href: "https://shop.example.com/wifi" }]);
  });

  it("服务端即使声称成功，点击仍然是未知结果", () => {
    expect(settleClick({
      recorded_as: "success",
      success: true,
      platform_result: "added",
      added: true,
      followed: true,
      lead_created: true,
      crm_imported: true,
      reward_triggered: true,
      publish_success: true,
    } as Parameters<typeof settleClick>[0])).toEqual({
      recordedAs: "click",
      success: false,
      platformResult: "unknown",
      added: false,
      followed: false,
      leadCreated: false,
      crmImported: false,
      rewardTriggered: false,
      publishSuccess: false,
      open: false,
    });
    expect(settleClick({
      recorded_as: "click",
      success: false,
      platform_result: "unknown",
    }).open).toBe(true);
  });

  it("五个附加动作都有客人能看懂的名字", () => {
    expect(guestActionLabel("wifi")).toBe("门店 WiFi");
    expect(guestActionLabel("navigate")).toBe("导航");
    expect(guestActionLabel("review")).toBe("写点评");
    expect(guestActionLabel("wecom")).toBe("加企微");
    expect(guestActionLabel("follow")).toBe("关注账号");
    expect(guestActionLabel("crm")).toBe("");
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

describe("390 宽的匿名活动路径", () => {
  const base = {
    width: 390,
    entry: "nfc",
    tenantFromQuery: "tnt_evil",
    merchant: "湖滨咖啡",
    store: "湖滨店",
    title: "到店送一杯",
    value: "到店可领一杯",
    consent: false,
    marketingOptIn: false,
    submitted: false,
    returnHref: "/c/back",
  };

  it("公共页保持一列，不是后台", () => {
    expect(publicVisitorColumns(390)).toBe(1);
    expect(publicVisitorColumns(1280)).toBe(1);
  });

  it("匿名进入后能看懂活动，拒绝营销仍可浏览，同意后提交是待同步，并且可以返回", () => {
    const entered = anonymousJourney(base);
    expect(entered).toMatchObject({
      width: 390,
      columns: 1,
      anonymous: true,
      channel: "nfc",
      tenantOverride: null,
      sections: ["store", "value", "actions", "lead", "next"],
      understood: ["湖滨咖啡", "湖滨店", "到店送一杯", "到店可领一杯"],
      consentShown: true,
      marketingRefused: true,
      browseBlocked: false,
      submitBlocked: true,
      outcome: "",
      returnShown: true,
      ecoNav: false,
      platformAccount: false,
      order: false,
    });

    const submitted = anonymousJourney({ ...base, entry: "qr", consent: true, submitted: true });
    expect(submitted.channel).toBe("qr");
    expect(submitted.submitBlocked).toBe(false);
    expect(submitted.marketingRefused).toBe(true);
    expect(submitted.browseBlocked).toBe(false);
    expect(submitted.outcome).toContain("已提交给湖滨咖啡");
    expect(submitted.outcome).toContain("待同步");
    expect(submitted.outcome).not.toContain("销售已收到");
    expect(submitted.platformAccount).toBe(false);
    expect(submitted.returnShown).toBe(true);

    const injected = anonymousJourney({ ...base, consent: true, submitted: true, confirmedReceiptCount: 3 });
    expect(injected.outcome).toBe("销售已收到 3");
    expect(submitted.outcome).not.toBe(injected.outcome);
  });
});

describe("降级状态各自可见", () => {
  it("弱网、暂停、结束、不存在、重复提交都不写成销售已收到", () => {
    const duplicate = leadOutcomeCopy({ merchant: "商家A", state: "duplicate", duplicate: true });
    const visible = [
      STATE_TEXT.network_error,
      STATE_ACTION.network_error,
      STATE_TEXT.paused,
      STATE_ACTION.paused,
      STATE_TEXT.ended,
      STATE_ACTION.ended,
      STATE_TEXT.not_found,
      STATE_ACTION.not_found,
      duplicate,
    ];
    expect(new Set(visible).size).toBe(visible.length);
    expect(STATE_TEXT.network_error).toContain("网络");
    expect(STATE_ACTION.network_error).toContain("重试");
    expect(STATE_TEXT.paused).toContain("暂停");
    expect(STATE_TEXT.ended).toContain("结束");
    expect(STATE_TEXT.not_found).toContain("不存在");
    expect(duplicate).toContain("没有再记一条");
    for (const text of visible) {
      expect(text).not.toContain("销售已收到");
    }
  });
});

describe("四个计数不合成一个转化", () => {
  it("曝光、点击、留资提交、CRM 接收保持原数", () => {
    expect(separateActivityCounts({
      exposure: 4,
      click: 3,
      leadSubmit: 2,
      crmReceived: 1,
    })).toEqual({
      exposure: 4,
      click: 3,
      leadSubmit: 2,
      crmReceived: 1,
      conversion: null,
    });
  });
});
