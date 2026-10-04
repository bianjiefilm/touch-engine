// HUI-2628 r2（裁决1）：移植自 public-ai@d01ec88 internal/uifinish（Go 版 Scan）。
// 业务仓禁导入 internal 包，故在本仓镜像；先例 = web/src/lib/page-map.ts（HUI-2624）。
// fail-close：结果为空只代表"这段源码没踩中规则"，不是产品通过声明。
// 与上游的已记录差异（spec §4.3，root 2026-10-04 追认）：scanFiles 文件级闸只适用
// FILE_LEVEL_RULES 8 条（上游 4 条正则对真实应用源码必然误报）。
// 计算布局白名单镜像上游 Go 版 12 项，无本地扩展（root 2026-10-04 修正，spec §4.4）。
import { readFileSync } from "node:fs";

export type RuleId =
  | "inline_style"
  | "raw_brand_status_hex"
  | "unstyled_native_control"
  | "todo_debug_test_copy"
  | "raw_json_or_http_error"
  | "hand_filled_tenant_or_internal_id"
  | "unmarked_mock_stub_banner"
  | "window_alert_confirm"
  | "off_design_system_table_form_button"
  | "missing_loading_empty_error"
  | "expired_activity_as_today_live"
  | "unknown_reward_success_green";

export const RULE_IDS: readonly RuleId[] = [
  "inline_style",
  "raw_brand_status_hex",
  "unstyled_native_control",
  "todo_debug_test_copy",
  "raw_json_or_http_error",
  "hand_filled_tenant_or_internal_id",
  "unmarked_mock_stub_banner",
  "window_alert_confirm",
  "off_design_system_table_form_button",
  "missing_loading_empty_error",
  "expired_activity_as_today_live",
  "unknown_reward_success_green",
];

// 文件级闸只适用这些规则；被排除的 4 条仍实现并有单测（见 spec §4.3 排除表）。
export const FILE_LEVEL_RULES: readonly RuleId[] = [
  "inline_style",
  "raw_brand_status_hex",
  "todo_debug_test_copy",
  "unmarked_mock_stub_banner",
  "window_alert_confirm",
  "missing_loading_empty_error",
  "expired_activity_as_today_live",
  "unknown_reward_success_green",
];

export interface UifinishFinding {
  ruleId: RuleId;
  line: number;
  message: string;
}

export interface UifinishFileFinding extends UifinishFinding {
  file: string;
  snippet?: string;
}

export type SurfaceKind = "fragment" | "main";

const TOUCH_SUCCESS_GREEN = "#065f46";
const TODAY_LIVE_COPY = "今天进行中";
const COMPUTED_LAYOUT_ATTR = "data-uifinish-computed-layout";
const TEST_BANNER_ATTR = "data-uifinish-test-banner";

// 计算布局白名单：镜像上游 Go 版 12 项，无本地扩展（root 2026-10-04 修正）。
const COMPUTED_LAYOUT_WHITELIST: readonly string[] = [
  "width",
  "height",
  "min-width",
  "min-height",
  "max-width",
  "max-height",
  "top",
  "right",
  "bottom",
  "left",
  "inset",
  "transform",
];

export function computedLayoutAllows(prop: string): boolean {
  const key = normalizeProp(prop);
  if (key === "") return false;
  return COMPUTED_LAYOUT_WHITELIST.some((item) => normalizeProp(item) === key);
}

function normalizeProp(prop: string): string {
  return prop.trim().toLowerCase().replace(/-/g, "");
}

const STYLE_ATTR_RE = /(?:^|[\s])style\s*=\s*/i;
const HEX_COLOR_RE = /#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{4}|[0-9a-fA-F]{3})\b/g;
const CSS_FUNC_RE = /\brgba?\s*\(|\bhsla?\s*\(/gi;
const COPY_RES: RegExp[] = [
  /\bTODO\b/gi,
  /\bFIXME\b/gi,
  /\bXXX\b/gi,
  /\bDEBUG\b/gi,
  /\bWIP\b/gi,
  /调试/g,
  /测试文案/g,
  /测试数据/g,
];
const RAW_ERROR_RES: RegExp[] = [
  /JSON\.stringify/g,
  /\binternal server error\b/gi,
  /\bbad gateway\b/gi,
  /\bbad request\b/gi,
  /\bhttp\/1\.[01]/gi,
  /\bstatusText\b/gi,
  /\{[^{}]*"(?:error|status|message)"\s*:/g,
];
const ID_RES: RegExp[] = [
  /tenant[_ -]?id/gi,
  /internal[_ -]?id/gi,
  /租户\s*ID/g,
  /内部\s*ID/g,
  /["']tenant["']\s*\+\s*["']_id["']/gi,
  /["']租户["']\s*\+\s*["']\s*ID["']/g,
];
const MOCK_RE = /\b(?:mock|stub)\b|模拟数据|假数据|桩数据/gi;
const MOCK_SPLIT_RE = /["']mo["']\s*\+\s*["']ck["']/gi;
const TEST_MARK_RE = new RegExp(`${TEST_BANNER_ATTR}|test-only|测试专用`, "i");
const ALERT_RES: RegExp[] = [
  /\bwindow\s*\.\s*alert\b/gi,
  /\bwindow\s*\.\s*confirm\b/gi,
  /\bwindow\s*\[\s*["']al["']\s*\+\s*["']ert["']\s*\]/gi,
  /\bwindow\s*\[\s*["']alert["']\s*\]/gi,
  /\bwindow\s*\[\s*["']confirm["']\s*\]/gi,
  /\bwindow\s*\?\.\s*alert\b/gi,
  /\bwindow\s*\?\.\s*confirm\b/gi,
  /(?:^|[^\w.])alert\s*\(/gi,
  /(?:^|[^\w.])confirm\s*\(/gi,
];
const SUCCESS_GREEN_RE = new RegExp(`tk-ok\\b|${TOUCH_SUCCESS_GREEN}\\b|成功绿|--tk-ok\\b`, "i");
const GREEN_CLASS_SPLIT_RE = /["']tk-["']\s*\+\s*["']ok["']/gi;
const EXPIRED_STATE_RE = /\bdata-state\s*=\s*(?:["']expired["']|\{\s*["']expired["']\s*\})/i;
const SPREAD_STYLE_COLOR_RE = /style\s*:\s*\{[^{}]*\bcolor\s*:/gi;
const DOM_STYLE_COLOR_RE = /\.style\s*\.\s*color\b/gi;
const TODAY_CONCAT_RES: RegExp[] = [
  /\{\s*["']今天["']\s*\+\s*["']进行中["']\s*\}/g,
  /["']今天["']\s*\+\s*["']进行中["']/g,
  /今天\s*\{\s*["']进行中["']\s*\}/g,
];

const NATIVE_CONTROLS = new Set(["button", "input", "select", "textarea"]);
const OFF_DESIGN_SYSTEM = new Set(["table", "form", "button"]);

function stateAttrPattern(name: string): RegExp {
  return new RegExp(`\\bdata-state\\s*=\\s*(?:["']${name}["']|\\{\\s*["']${name}["']\\s*\\})`, "i");
}

const STATE_RES: Record<string, RegExp> = {
  loading: stateAttrPattern("loading"),
  empty: stateAttrPattern("empty"),
  error: stateAttrPattern("error"),
};

// ---- Scan（对齐 Go Scan 的规则顺序与消息） --------------------------------

export function scanSource(src: string, kind: SurfaceKind = "fragment"): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  out.push(...findInlineStyles(src));
  out.push(...findRawColors(src));
  out.push(...findTags(src));
  out.push(...findPatterns(src, "todo_debug_test_copy", "TODO、debug 或 test 文案", COPY_RES));
  out.push(...findPatterns(src, "raw_json_or_http_error", "原始 JSON 或 HTTP 错误", RAW_ERROR_RES));
  out.push(...findPatterns(src, "hand_filled_tenant_or_internal_id", "手填 tenant id 或 internal id", ID_RES));
  out.push(...findUnmarkedMockBanners(src));
  out.push(...findAlertConfirm(src));
  out.push(...findMissingPageStates(src, kind));
  out.push(...findExpiredAsTodayLive(src));
  out.push(...findUnknownRewardSuccessGreen(src));
  return out.sort(
    (a, b) => a.line - b.line || a.ruleId.localeCompare(b.ruleId) || a.message.localeCompare(b.message),
  );
}

// ---- inline_style ----------------------------------------------------------

function findInlineStyles(src: string): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  eachStartTag(src, (_name, body, at) => {
    for (const value of styleValues(body)) {
      const problem = styleViolates(body, value);
      if (problem) out.push({ ruleId: "inline_style", line: lineOf(src, at), message: problem });
    }
  });
  const masked = maskNonRendered(src);
  for (const m of masked.matchAll(SPREAD_STYLE_COLOR_RE)) {
    out.push({ ruleId: "inline_style", line: lineOf(masked, m.index ?? 0), message: "展开的 style 对象写了 color，计算布局标记不能放行" });
  }
  for (const m of masked.matchAll(DOM_STYLE_COLOR_RE)) {
    out.push({ ruleId: "inline_style", line: lineOf(masked, m.index ?? 0), message: "运行时 style.color，计算布局标记不能放行" });
  }
  return out;
}

function styleViolates(tag: string, value: string): string | null {
  if (!tag.includes(COMPUTED_LAYOUT_ATTR)) {
    return `行内样式缺少 ${COMPUTED_LAYOUT_ATTR}，计算布局白名单未显式套用`;
  }
  const body = jsxObjectBody(value);
  if (body !== null) return jsxStyleProblem(body);
  return cssStyleProblem(value);
}

function jsxObjectBody(value: string): string | null {
  let inner = balancedInner(value.trim());
  if (inner === null) return null;
  return balancedInner(inner.trim());
}

function balancedInner(s: string): string | null {
  s = s.trim();
  if (s === "" || s[0] !== "{") return null;
  let depth = 0;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (quote !== "") {
      if (c === "\\" && i + 1 < s.length) {
        i++;
        continue;
      }
      if (c === quote) quote = "";
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      quote = c;
      continue;
    }
    if (c === "{") {
      depth++;
      continue;
    }
    if (c === "}") {
      depth--;
      if (depth === 0) return s.slice(1, i);
    }
  }
  return null;
}

function jsxStyleProblem(body: string): string | null {
  let saw = false;
  for (let i = 0; i < body.length; ) {
    i = skipWS(body, i);
    if (i >= body.length || body[i] === "}") break;
    if (body[i] === ",") {
      i++;
      continue;
    }
    if (body.startsWith("...", i)) return "展开不是显式计算布局白名单属性";
    if (body[i] === "[") return "计算属性名不是显式计算布局白名单属性";
    if (!isNameStart(body[i])) return "行内样式不是显式计算布局白名单属性";
    const [name, next] = readIdent(body, i);
    const j = skipWS(body, next);
    if (j < body.length && body[j] === ":") {
      if (!computedLayoutAllows(name)) return `属性 ${name} 不在显式计算布局白名单内`;
      saw = true;
      i = skipJSXValue(body, j + 1);
      continue;
    }
    if (!computedLayoutAllows(name)) return `简写属性 ${name} 不在显式计算布局白名单内`;
    saw = true;
    i = next;
  }
  if (!saw) return "行内样式没有显式列出计算布局白名单属性";
  return null;
}

function cssStyleProblem(value: string): string | null {
  let raw = value.trim();
  if (raw.length >= 2 && ((raw[0] === '"' && raw[raw.length - 1] === '"') || (raw[0] === "'" && raw[raw.length - 1] === "'"))) {
    raw = raw.slice(1, -1);
  }
  let saw = false;
  for (const part of raw.split(";")) {
    const p = part.trim();
    if (p === "") continue;
    const colon = p.indexOf(":");
    if (colon <= 0) return "行内样式没有显式列出计算布局白名单属性";
    const name = p.slice(0, colon).trim();
    if (!computedLayoutAllows(name)) return `属性 ${name} 不在显式计算布局白名单内`;
    saw = true;
  }
  if (!saw) return "行内样式没有显式列出计算布局白名单属性";
  return null;
}

function readIdent(s: string, i: number): [string, number] {
  let j = i + 1;
  while (j < s.length && (isNameCont(s[j]) || s[j] === "-")) j++;
  return [s.slice(i, j), j];
}

function skipWS(s: string, i: number): number {
  while (i < s.length && (s[i] === " " || s[i] === "\n" || s[i] === "\t" || s[i] === "\r")) i++;
  return i;
}

function skipJSXValue(s: string, i: number): number {
  let depth = 0;
  let quote = "";
  while (i < s.length) {
    const c = s[i];
    if (quote !== "") {
      if (c === "\\" && i + 1 < s.length) {
        i += 2;
        continue;
      }
      if (c === quote) quote = "";
      i++;
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      quote = c;
      i++;
      continue;
    }
    if (c === "{" || c === "[" || c === "(") {
      depth++;
      i++;
      continue;
    }
    if (c === "}" || c === "]" || c === ")") {
      if (depth === 0) return i;
      depth--;
      i++;
      continue;
    }
    if (c === "," && depth === 0) return i;
    i++;
  }
  return i;
}

function styleValues(tag: string): string[] {
  const vals: string[] = [];
  let offset = 0;
  while (offset < tag.length) {
    const rest = tag.slice(offset);
    const m = STYLE_ATTR_RE.exec(rest);
    if (!m) break;
    const valueAt = offset + m.index + m[0].length;
    if (valueAt >= tag.length) break;
    const [val, n] = readStyleValue(tag.slice(valueAt));
    if (n <= 0) {
      offset = valueAt + 1;
      continue;
    }
    if (val !== "") vals.push(val);
    offset = valueAt + n;
  }
  return vals;
}

function readStyleValue(s: string): [string, number] {
  if (s === "") return ["", 0];
  const c0 = s[0];
  if (c0 === '"' || c0 === "'" || c0 === "`") {
    for (let i = 1; i < s.length; i++) {
      if (s[i] === "\\" && i + 1 < s.length) {
        i++;
        continue;
      }
      if (s[i] === c0) return [s.slice(0, i + 1), i + 1];
    }
    return [s, s.length];
  }
  if (c0 === "{") {
    let depth = 0;
    let quote = "";
    for (let i = 0; i < s.length; i++) {
      const c = s[i];
      if (quote !== "") {
        if (c === "\\" && i + 1 < s.length) {
          i++;
          continue;
        }
        if (c === quote) quote = "";
        continue;
      }
      if (c === '"' || c === "'" || c === "`") {
        quote = c;
        continue;
      }
      if (c === "{") {
        depth++;
        continue;
      }
      if (c === "}") {
        depth--;
        if (depth === 0) return [s.slice(0, i + 1), i + 1];
      }
    }
    return [s, s.length];
  }
  return ["", 1];
}

// ---- raw_brand_status_hex --------------------------------------------------

function findRawColors(src: string): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  for (const m of src.matchAll(HEX_COLOR_RE)) {
    out.push({ ruleId: "raw_brand_status_hex", line: lineOf(src, m.index ?? 0), message: `裸品牌或状态色 ${m[0]}` });
  }
  for (const m of src.matchAll(CSS_FUNC_RE)) {
    out.push({ ruleId: "raw_brand_status_hex", line: lineOf(src, m.index ?? 0), message: `裸品牌或状态色 ${m[0].trim()}` });
  }
  return out;
}

// ---- 标签类规则 ------------------------------------------------------------

function findTags(src: string): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  eachStartTag(src, (name, _body, at) => {
    if (NATIVE_CONTROLS.has(name)) {
      out.push({ ruleId: "unstyled_native_control", line: lineOf(src, at), message: `原生 ${name} 未走设计系统控件` });
    }
    if (OFF_DESIGN_SYSTEM.has(name)) {
      out.push({ ruleId: "off_design_system_table_form_button", line: lineOf(src, at), message: `未经设计系统的 ${name}` });
    }
  });
  return out;
}

function findPatterns(src: string, ruleId: RuleId, label: string, patterns: RegExp[]): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  for (const pattern of patterns) {
    pattern.lastIndex = 0;
    for (const m of src.matchAll(pattern)) {
      out.push({ ruleId, line: lineOf(src, m.index ?? 0), message: `${label} ${oneLine(m[0])}` });
    }
  }
  return out;
}

// ---- unmarked_mock_stub_banner --------------------------------------------

function findUnmarkedMockBanners(src: string): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  const masked = maskNonRendered(src);
  const locs: number[] = [];
  for (const m of masked.matchAll(MOCK_RE)) locs.push(m.index ?? 0);
  for (const m of masked.matchAll(MOCK_SPLIT_RE)) locs.push(m.index ?? 0);
  for (const at of locs) {
    const ctx = elementContext(masked, at);
    if (TEST_MARK_RE.test(ctx)) continue;
    out.push({ ruleId: "unmarked_mock_stub_banner", line: lineOf(masked, at), message: "mock/stub banner 未标明测试" });
  }
  return out;
}

// ---- window_alert_confirm --------------------------------------------------

function findAlertConfirm(src: string): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  for (const pattern of ALERT_RES) {
    pattern.lastIndex = 0;
    for (const m of src.matchAll(pattern)) {
      out.push({ ruleId: "window_alert_confirm", line: lineOf(src, m.index ?? 0), message: `使用了 ${oneLine(m[0])}` });
    }
  }
  return out;
}

// ---- missing_loading_empty_error ------------------------------------------

function findMissingPageStates(src: string, kind: SurfaceKind): UifinishFinding[] {
  if (!isMainSurface(src, kind)) return [];
  const rendered = maskAnglesInStrings(maskNonRendered(src));
  const missing: string[] = [];
  for (const name of ["loading", "empty", "error"]) {
    if (!renderedHasState(rendered, name)) missing.push(name);
  }
  if (missing.length === 0) return [];
  return [{ ruleId: "missing_loading_empty_error", line: 1, message: `主页面缺少 ${missing.join("、")}` }];
}

function renderedHasState(src: string, name: string): boolean {
  return eachStartTagSome(src, (_name, body) => STATE_RES[name].test(body));
}

function isMainSurface(src: string, kind: SurfaceKind): boolean {
  if (kind === "main") return true;
  return src.includes('data-uifinish-surface="main"') || src.includes("data-uifinish-surface='main'");
}

// ---- expired_activity_as_today_live ---------------------------------------

function findExpiredAsTodayLive(src: string): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  const masked = maskNonRendered(src);
  eachOwnedElement(masked, (openTag, inner, at) => {
    if (!EXPIRED_STATE_RE.test(openTag) || !containsTodayLive(inner)) return;
    out.push({ ruleId: "expired_activity_as_today_live", line: lineOf(src, at), message: "过期活动被写成「今天进行中」" });
  });
  return out;
}

function containsTodayLive(inner: string): boolean {
  let text = stripTags(inner);
  if (text.includes(TODAY_LIVE_COPY)) return true;
  for (const pattern of TODAY_CONCAT_RES) {
    text = text.replace(pattern, TODAY_LIVE_COPY);
  }
  return text.includes(TODAY_LIVE_COPY);
}

// ---- unknown_reward_success_green -----------------------------------------

function findUnknownRewardSuccessGreen(src: string): UifinishFinding[] {
  const out: UifinishFinding[] = [];
  eachOwnedElement(src, (openTag, inner, at) => {
    const scope = foldGreenClass(openTag + inner);
    if (!unknownReward(scope) || !SUCCESS_GREEN_RE.test(scope)) return;
    out.push({ ruleId: "unknown_reward_success_green", line: lineOf(src, at), message: "未知奖励用了成功绿" });
  });
  return out;
}

function foldGreenClass(s: string): string {
  return s.replace(GREEN_CLASS_SPLIT_RE, "tk-ok");
}

function unknownReward(ctx: string): boolean {
  if (ctx.includes("未知奖励")) return true;
  const lower = ctx.toLowerCase();
  const unknown = lower.includes("unknown") || ctx.includes("未知");
  const reward = lower.includes("reward") || ctx.includes("奖励") || lower.includes("grant");
  return unknown && reward;
}

// ---- 源码遍历辅助（对齐 scan.go） -------------------------------------------

function eachStartTag(src: string, fn: (name: string, body: string, at: number) => void): void {
  for (let i = 0; i < src.length; i++) {
    if (src[i] !== "<") continue;
    if (i + 1 >= src.length || !isNameStart(src[i + 1])) continue;
    let j = i + 1;
    while (j < src.length && isNameCont(src[j])) j++;
    const end = scanTagEnd(src, j);
    fn(src.slice(i + 1, j), src.slice(i, end), i);
    if (end > i) i = end - 1;
  }
}

function eachStartTagSome(src: string, pred: (name: string, body: string) => boolean): boolean {
  let found = false;
  eachStartTag(src, (name, body) => {
    if (!found && pred(name, body)) found = true;
  });
  return found;
}

function scanTagEnd(src: string, i: number): number {
  let quote = "";
  let brace = 0;
  for (; i < src.length; i++) {
    const c = src[i];
    if (quote !== "") {
      if (c === "\\" && i + 1 < src.length) {
        i++;
        continue;
      }
      if (c === quote) quote = "";
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      quote = c;
      continue;
    }
    if (c === "{") {
      brace++;
      continue;
    }
    if (c === "}" && brace > 0) {
      brace--;
      continue;
    }
    if (c === ">" && brace === 0) return i + 1;
  }
  return src.length;
}

function eachOwnedElement(src: string, fn: (openTag: string, inner: string, at: number) => void): void {
  for (let i = 0; i < src.length; i++) {
    if (src[i] !== "<" || i + 1 >= src.length || !isNameStart(src[i + 1])) continue;
    const [openTag, inner, openEnd] = elementParts(src, i);
    if (openEnd <= i) continue;
    fn(openTag, inner, i);
    i = openEnd - 1;
  }
}

function elementParts(src: string, openAt: number): [string, string, number] {
  let j = openAt + 1;
  while (j < src.length && isNameCont(src[j])) j++;
  const name = src.slice(openAt + 1, j);
  const openEnd = scanTagEnd(src, j);
  const openTag = src.slice(openAt, openEnd);
  if (openTag.trimEnd().endsWith("/>")) return [openTag, "", openEnd];
  const closeStart = findMatchingClose(src, openEnd, name);
  if (closeStart < 0) return [openTag, "", openEnd];
  return [openTag, src.slice(openEnd, closeStart), openEnd];
}

function findMatchingClose(src: string, from: number, name: string): number {
  let depth = 1;
  for (let i = from; i < src.length; ) {
    if (src[i] !== "<") {
      i++;
      continue;
    }
    if (i + 1 < src.length && src[i + 1] === "/") {
      let j = i + 2;
      while (j < src.length && isNameCont(src[j])) j++;
      let end = j;
      if (j < src.length && src[j] === ">") end = j + 1;
      if (j >= i + 2 && src.slice(i + 2, j) === name) {
        depth--;
        if (depth === 0) return i;
      }
      i = end <= i ? i + 1 : end;
      continue;
    }
    if (i + 1 < src.length && isNameStart(src[i + 1])) {
      let j = i + 1;
      while (j < src.length && isNameCont(src[j])) j++;
      if (src.slice(i + 1, j) === name) {
        const end = scanTagEnd(src, j);
        if (!src.slice(i, end).trimEnd().endsWith("/>")) depth++;
        i = end <= i ? i + 1 : end;
        continue;
      }
    }
    i++;
  }
  return -1;
}

function stripTags(s: string): string {
  let out = "";
  for (let i = 0; i < s.length; ) {
    if (s[i] === "<") {
      const end = scanTagEnd(s, i + 1);
      if (end <= i) {
        out += s[i];
        i++;
        continue;
      }
      i = end;
      continue;
    }
    out += s[i];
    i++;
  }
  return out;
}

// ---- 屏蔽非渲染内容（对齐 maskNonRendered） ---------------------------------

function maskNonRendered(src: string): string {
  // 对齐上游 Go 版：引号内字符只跳过（保留原文），仅清空 //、/* */、JSX/HTML 注释与模板字符串。
  const b = src.split("");
  let quote = "";
  for (let i = 0; i < b.length; ) {
    if (quote !== "") {
      if (b[i] === "\\" && i + 1 < b.length) {
        i += 2;
        continue;
      }
      if (b[i] === quote) quote = "";
      i++;
      continue;
    }
    if (b[i] === "/" && b[i + 1] === "/") {
      let j = i;
      while (j < b.length && b[j] !== "\n") {
        b[j] = " ";
        j++;
      }
      i = j;
      continue;
    }
    if (b[i] === "/" && b[i + 1] === "*") {
      i = blankBlockComment(b, i);
      continue;
    }
    if (jsxCommentAt(b, i)) {
      i = blankJSXComment(b, i);
      continue;
    }
    if (b[i] === "<" && b[i + 1] === "!" && b[i + 2] === "-" && b[i + 3] === "-") {
      i = blankUntil(b, i, "-->");
      continue;
    }
    if (b[i] === "`") {
      let j = i + 1;
      while (j < b.length && b[j] !== "`") {
        if (b[j] === "\\" && j + 1 < b.length) {
          b[j] = " ";
          b[j + 1] = " ";
          j += 2;
          continue;
        }
        b[j] = " ";
        j++;
      }
      if (j < b.length) {
        b[j] = " ";
        j++;
      }
      i = j;
      continue;
    }
    if (b[i] === '"' || b[i] === "'") {
      quote = b[i];
    }
    i++;
  }
  return b.join("");
}

function jsxCommentAt(b: string[], i: number): boolean {
  if (i >= b.length || b[i] !== "{") return false;
  let j = i + 1;
  while (j < b.length && isSpace(b[j])) j++;
  return j + 1 < b.length && b[j] === "/" && b[j + 1] === "*";
}

function blankJSXComment(b: string[], start: number): number {
  let j = start + 1;
  while (j < b.length && isSpace(b[j])) j++;
  const rel = b.slice(j).join("").indexOf("*/");
  let stop = b.length;
  if (rel >= 0) {
    stop = j + rel + 2;
    let k = stop;
    while (k < b.length && isSpace(b[k])) k++;
    if (k < b.length && b[k] === "}") stop = k + 1;
  }
  for (let i = start; i < stop; i++) b[i] = " ";
  return stop;
}

function blankBlockComment(b: string[], start: number): number {
  const rel = b.slice(start).join("").indexOf("*/");
  let stop = b.length;
  if (rel >= 0) stop = start + rel + 2;
  for (let i = start; i < stop; i++) b[i] = " ";
  return stop;
}

function blankUntil(b: string[], start: number, endMark: string): number {
  const rel = b.slice(start).join("").indexOf(endMark);
  let stop = b.length;
  if (rel >= 0) stop = start + rel + endMark.length;
  for (let i = start; i < stop; i++) b[i] = " ";
  return stop;
}

function maskAnglesInStrings(src: string): string {
  const b = src.split("");
  let quote = "";
  for (let i = 0; i < b.length; i++) {
    if (quote !== "") {
      if (b[i] === "\\" && i + 1 < b.length) {
        i++;
        continue;
      }
      if (b[i] === quote) {
        quote = "";
        continue;
      }
      if (b[i] === "<") b[i] = " ";
      continue;
    }
    if (b[i] === '"' || b[i] === "'") quote = b[i];
  }
  return b.join("");
}

function elementContext(src: string, idx: number): string {
  if (idx < 0) idx = 0;
  if (idx > src.length) idx = src.length;
  let start = 0;
  const lastOpen = src.lastIndexOf("<", idx);
  if (lastOpen >= 0) start = lastOpen;
  if (start + 1 < src.length && src[start] === "<" && src[start + 1] === "/") {
    start = idx;
    const gt = src.lastIndexOf(">", idx);
    if (gt >= 0) start = gt + 1;
  }
  if (start > 0) {
    const gt = src.lastIndexOf(">", idx);
    if (gt >= 0 && start === gt + 1) {
      const opener = src.lastIndexOf("<", start);
      if (opener >= 0 && opener + 1 < src.length && src[opener + 1] !== "/") start = opener;
    }
  }
  let end = src.length;
  if (idx < src.length) {
    const p = src.indexOf("<", idx);
    if (p >= 0) end = idx + (p - idx);
  }
  if (end < start) end = src.length;
  return src.slice(start, end);
}

// ---- 杂项 -------------------------------------------------------------------

function lineOf(src: string, idx: number): number {
  if (idx < 0) idx = 0;
  if (idx > src.length) idx = src.length;
  return src.slice(0, idx).split("\n").length;
}

function oneLine(s: string): string {
  const flat = s.replace(/\n/g, " ");
  return flat.length > 80 ? flat.slice(0, 80) : flat;
}

function isNameStart(c: string): boolean {
  return (c >= "A" && c <= "Z") || (c >= "a" && c <= "z");
}

function isNameCont(c: string): boolean {
  return isNameStart(c) || (c >= "0" && c <= "9");
}

function isSpace(c: string): boolean {
  return c === " " || c === "\t" || c === "\n" || c === "\r";
}

// ---- 文件级入口 -------------------------------------------------------------

export function scanFiles(files: string[]): UifinishFileFinding[] {
  const out: UifinishFileFinding[] = [];
  for (const file of files) {
    let src: string;
    try {
      src = readFileSync(file, "utf8");
    } catch {
      continue; // 读不到的文件跳过；gate 用例的清单是仓库内实际存在的文件
    }
    for (const f of scanSource(src, "fragment")) {
      if (!FILE_LEVEL_RULES.includes(f.ruleId)) continue;
      const snippet = oneLine(src.split("\n")[f.line - 1] ?? "");
      out.push({ ...f, file, snippet });
    }
  }
  return out;
}
