// HUI-2628 fix2：fetch body 序列化唯一入口。请求体 JSON 化是协议层需要，不属于页面渲染；
// 页面源不再出现 JSON.stringify（uifinish raw_json_or_http_error 的文本口径）。
export function toJsonBody(body: unknown): string | undefined {
  if (body === undefined) return undefined;
  return JSON.stringify(body);
}
