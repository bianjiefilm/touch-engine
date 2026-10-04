import { describe, expect, it } from "vitest";
import { takenList } from "@/lib/campaign-detail";

describe("takenList 三态", () => {
  it("ok=false → error，即使带了 items", () => {
    expect(takenList({ ok: false, items: [{ id: "x" }] })).toEqual({ state: "error", items: [] });
  });

  it("items=null → empty", () => {
    expect(takenList({ ok: true, items: null })).toEqual({ state: "empty", items: [] });
  });

  it("items 非数组 → error", () => {
    expect(takenList({ ok: true, items: { a: 1 } })).toEqual({ state: "error", items: [] });
  });

  it("空数组 → empty", () => {
    expect(takenList({ ok: true, items: [] })).toEqual({ state: "empty", items: [] });
  });

  it("非空数组 → ready 且元素原样透传", () => {
    const rows = [{ id: "l1" }, { id: "l2" }];
    expect(takenList({ ok: true, items: rows })).toEqual({ state: "ready", items: rows });
  });
});
