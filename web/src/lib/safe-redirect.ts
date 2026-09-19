// HUI-1664 FEAT-0165 受控跳转守卫。
//
// 红线:公共活动页若出现任何 next/redirect/return 参数,目标只允许是本站内
// 相对路径(^/[a-z0-9/_-]*$,小写、无协议、无查询、无编码);不合法一律丢弃,
// 默认落地本活动页。任何参数都不可能把游客带离 H5 或送进后台路由。

export const REDIRECT_KEYS = ["next", "redirect", "return"] as const;

// 站内相对路径白名单:必须以单个 "/" 开头;只允许小写字母、数字、斜杠、
// 下划线、连字符。 "." 协议相对 "//"、绝对 URL、反斜杠、%编码、大写、
// 查询/片段字符全部不匹配。
const IN_SITE_PATH = /^\/[a-z0-9/_-]*$/;

export function sanitizeInSitePath(raw: string | null | undefined): string | null {
  if (typeof raw !== "string") return null;
  const v = raw.trim();
  if (!v) return null;
  // 任何百分号编码都被视为可疑:解码后与原值不同即丢弃(覆盖 %2F、%252F
  // 双重编码等绕过)。解码失败同样丢弃。
  if (/%[0-9a-fA-F]{2}/.test(v)) {
    try {
      if (decodeURIComponent(v) !== v) return null;
    } catch {
      return null;
    }
  }
  if (v.includes("\\") || v.includes("//")) return null;
  if (!IN_SITE_PATH.test(v)) return null;
  return v;
}

// 从查询参数收集跳转目标:第一个非空的 next/redirect/return 键胜出;
// 值不合法 → null(默认落地本活动页)。get 为取参函数,便于测试与
// useSearchParams 两种调用方复用。
export function inSiteTargetFromParams(get: (key: string) => string | null | undefined): string | null {
  for (const key of REDIRECT_KEYS) {
    const raw = get(key);
    if (typeof raw === "string" && raw !== "") {
      return sanitizeInSitePath(raw);
    }
  }
  return null;
}
