import { describe, expect, it } from "vitest";
import {
  STATE_ACTION,
  STATE_TEXT,
  classifyEntry,
  failureState,
} from "../src/lib/public-state";

// HUI-1664:五态 + 网络失败/入口不支持 —— 每个状态都有准确文案与可恢复动作。

describe("五态 + 兜底文案", () => {
  const cases: Array<[string, string, string]> = [
    // [state, 文案存在, 可恢复动作文案存在]
    ["available", "活动进行中", ""],
    ["link_disabled", "链接已停用", "请联系商家获取最新的活动入口"],
    ["not_started", "活动尚未开始", "请按活动时间再来看看"],
    ["expired", "活动已结束", "活动已结束,感谢关注"],
    ["paused", "活动暂停中", "请稍后再来"],
    ["draft", "活动未发布", "活动还未发布,请稍后再来"],
    ["ended", "活动已结束", "感谢关注,请留意商家后续活动"],
    ["not_found", "活动不存在", "请核对二维码是否正确,或联系商家"],
    ["network_error", "网络异常,活动加载失败", "请检查网络后重试"],
    ["entry_unsupported", "入口方式不支持", "请通过商家提供的最新二维码或碰一碰标签重新进入"],
  ];
  for (const [state, text, action] of cases) {
    it(`${state} 文案分支`, () => {
      expect(STATE_TEXT[state]).toBe(text);
      expect(STATE_ACTION[state]).toBe(action);
    });
  }
});

describe("classifyEntry 入口白名单", () => {
  it("缺省/空 → web(直接访问,支持)", () => {
    expect(classifyEntry(null)).toBe("web");
    expect(classifyEntry(undefined)).toBe("web");
    expect(classifyEntry("")).toBe("web");
  });
  it("白名单内 → 原样返回", () => {
    expect(classifyEntry("qr")).toBe("qr");
    expect(classifyEntry("nfc")).toBe("nfc");
    expect(classifyEntry("web")).toBe("web");
  });
  it("显式未知值 → unsupported(含大小写变体与注入形态)", () => {
    expect(classifyEntry("miniprogram")).toBe("unsupported");
    expect(classifyEntry("QR")).toBe("unsupported");
    expect(classifyEntry("../../admin")).toBe("unsupported");
    expect(classifyEntry("qr?x=1")).toBe("unsupported");
  });
});

describe("failureState 网络失败归一", () => {
  it("fetch 抛错(status=null)→ network_error", () => {
    expect(failureState(null)).toBe("network_error");
  });
  it("BFF/上游 502/503/504 → network_error", () => {
    expect(failureState(502)).toBe("network_error");
    expect(failureState(503)).toBe("network_error");
    expect(failureState(504)).toBe("network_error");
  });
  it("服务端正常作答(404/200)不冒充网络故障", () => {
    expect(failureState(404)).toBe("not_found");
    expect(failureState(200)).toBe("not_found");
    expect(failureState(400)).toBe("not_found");
  });
});
