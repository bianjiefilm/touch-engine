import { describe, expect, it } from "vitest";
import { toJsonBody } from "../src/lib/http-json";

// HUI-2628 fix2：fetch body 序列化收口（页面源不再出现 JSON.stringify）。
describe("toJsonBody", () => {
  it("undefined 透传，对象序列化", () => {
    expect(toJsonBody(undefined)).toBeUndefined();
    expect(toJsonBody({ a: 1 })).toBe('{"a":1}');
  });
});
