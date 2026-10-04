import { describe, expect, it } from "vitest";
import {
  activityJumpActions,
  presentPrivateDomain,
  privateDomainClick,
  privateDomainLabel,
  privateDomainNote,
  settlePrivateDomainClick,
} from "../src/lib/private-domain";
import { presentGuestActions } from "../src/lib/visitor-experience";

describe("只展示当前渠道打得开的企微或社群", () => {
  it("非企微地址、未就绪和当前渠道打不开的入口不出现，也不标成已接通", () => {
    const guide = presentPrivateDomain({
      entries: [
        { kind: "wecom", available: true, result: "ready", href: "https://work.weixin.qq.com/ca/demo", event: "click_wecom" },
        { kind: "community", available: true, result: "ready", href: "https://shop.example.com/group", event: "click_community" },
        { kind: "wecom", available: true, result: "ready", href: "https://work.weixin.qq.com/gm/group-demo", event: "click_wecom" },
      ],
      connected: true,
      redemption: "redeemed",
    } as Parameters<typeof presentPrivateDomain>[0], "web");
    expect(guide.entries).toEqual([
      { kind: "wecom", href: "https://work.weixin.qq.com/ca/demo", event: "click_wecom" },
    ]);
    expect(guide.connected).toBe(false);
    expect(guide.redemption).toBe("unknown");
    expect(guide.entries).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ href: "https://shop.example.com/group" }),
    ]));
  });

  it("小程序渠道没有打开能力时不展示入口", () => {
    const guide = presentPrivateDomain({
      entries: [
        { kind: "wecom", available: true, result: "ready", href: "https://work.weixin.qq.com/ca/demo", event: "click_wecom" },
        { kind: "community", available: true, result: "ready", href: "https://work.weixin.qq.com/gm/demo", event: "click_community" },
      ],
    }, "miniprogram");
    expect(guide.entries).toEqual([]);
    expect(guide.connected).toBe(false);
    expect(guide.redemption).toBe("unknown");
  });

  it("没有入口时不出现私域已接通", () => {
    const guide = presentPrivateDomain(null, "web");
    expect(guide.entries).toEqual([]);
    expect(guide.connected).toBe(false);
    expect(privateDomainNote(guide)).not.toContain("私域已接通");
    expect(privateDomainNote(guide)).not.toContain("核销成功");
  });
});

describe("click_wecom 只是点击", () => {
  it("点击记录不是添加、进群、新联系人或核销", () => {
    expect(privateDomainClick("wecom")).toEqual({
      kind: "wecom",
      event: "click_wecom",
      recordedAs: "click",
      success: false,
      platformResult: "unknown",
      redemption: "unknown",
    });
    expect(privateDomainClick("community").event).toBe("click_community");
  });

  it("服务端即使声称加群成功，页面仍然保持未知并且不打开", () => {
    expect(settlePrivateDomainClick({
      event: "click_wecom",
      recorded_as: "success",
      success: true,
      platform_result: "added",
      added: true,
      joined: true,
      contact_created: true,
      redemption: "redeemed",
      connected: true,
    } as Parameters<typeof settlePrivateDomainClick>[0])).toEqual({
      event: "click_wecom",
      recordedAs: "click",
      success: false,
      platformResult: "unknown",
      added: false,
      joined: false,
      contactCreated: false,
      redemption: "unknown",
      connected: false,
      open: false,
    });
  });

  it("诚实的点击结果才允许打开，文案不说已加好友或已核销", () => {
    const settled = settlePrivateDomainClick({
      event: "click_wecom",
      recorded_as: "click",
      success: false,
      platform_result: "unknown",
      redemption: "unknown",
      connected: false,
    });
    expect(settled.open).toBe(true);
    const note = privateDomainNote({ entries: [], connected: false, redemption: "unknown" }, settled);
    expect(note).toBe("这一下只是点击。还不是添加成功、进群成功或新增联系人。核销结果未知。");
    expect(note).not.toContain("核销成功");
    expect(note).not.toContain("私域已接通");
  });
});

describe("企微和社群不再混进普通跳转", () => {
  it("普通动作留下门店 WiFi，企微改走私域入口", () => {
    const jumps = activityJumpActions(presentGuestActions([
      { kind: "wifi", available: true, result: "ready", href: "https://shop.example.com/wifi" },
      { kind: "wecom", available: true, result: "ready", href: "https://shop.example.com/wecom" },
    ]));
    expect(jumps).toEqual([{ kind: "wifi", href: "https://shop.example.com/wifi" }]);
  });

  it("入口名称只描述动作，不描述结果", () => {
    expect(privateDomainLabel("wecom")).toBe("加企微");
    expect(privateDomainLabel("community")).toBe("进社群");
    expect(privateDomainLabel("crm")).toBe("");
  });
});
