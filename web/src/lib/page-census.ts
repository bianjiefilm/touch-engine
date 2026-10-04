// HUI-2620 第一片：只给账上已经存在的用户路由做普查。
// 一条路由一条。surface 只按路径和模式编号归类。
// 半成品标记只扫该 page 文件，不读 layout、组件或同目录其它文件。
// token、状态、响应式、无障碍、截图都不测。不改页面。
// 这一片不能把 HUI-2620、HUI-2619、HUI-2628 或 HUI-2748 标 Done。

import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { pageLedger } from "./page-map";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const appDir = path.join(webRoot, "src/app");
const pageExtensions = ["tsx", "ts", "jsx", "js"] as const;

export const surfaceList = ["portal", "work", "editor", "public", "admin"] as const;

export type Surface = (typeof surfaceList)[number];

export const markerKindList = ["TODO", "FIXME", "inline style", "接口说明", "架构说明"] as const;

export type Marker = (typeof markerKindList)[number];

export interface PageCensus {
  route: string;
  id: string;
  file: string;
  surface: Surface;
  stack: "next";
  markers: Marker[];
  token_source: "not_measured";
  states: "not_measured";
  responsive: "not_measured";
  accessibility: "not_measured";
  screenshot: "not_measured";
}

// 路径里出现 /admin 优先。其后才看模式编号。比较精确。
export function classifySurface(route: string, patternId: string): Surface {
  if (route.includes("/admin")) return "admin";
  if (patternId === "public_consumer") return "public";
  if (patternId === "editor_shell") return "editor";
  if (patternId === "portal" || patternId === "login") return "portal";
  return "work";
}

function hasToken(source: string, token: string): boolean {
  const pattern = new RegExp(`(?:^|[^A-Za-z0-9_])${token}(?![A-Za-z0-9_])`);
  return pattern.test(source);
}

export function scanMarkers(source: string): Marker[] {
  const found: Marker[] = [];
  if (hasToken(source, "TODO")) found.push("TODO");
  if (hasToken(source, "FIXME")) found.push("FIXME");
  if (/(?:^|[^A-Za-z0-9_])style\s*=/.test(source)) found.push("inline style");
  if (source.includes("接口说明")) found.push("接口说明");
  if (source.includes("架构说明")) found.push("架构说明");
  return found;
}

const markerBook: readonly { route: string; markers: readonly Marker[] }[] = [
  { route: "/", markers: [] },
  { route: "/admin", markers: ["inline style"] },
  { route: "/admin/preview", markers: ["inline style"] },
  { route: "/work/stores", markers: [] },
  { route: "/work/campaigns", markers: [] },
  { route: "/work/campaigns/[id]", markers: [] },
  { route: "/work/materials", markers: [] },
  { route: "/work/rewards", markers: [] },
  { route: "/work/analytics", markers: [] },
  { route: "/c/[code]", markers: [] },
  { route: "/c/[code]/contact", markers: [] },
];

function sameMarkers(recorded: readonly Marker[], scanned: readonly Marker[]): boolean {
  return recorded.length === scanned.length && recorded.every((marker, index) => marker === scanned[index]);
}

function markersFor(route: string): Marker[] {
  const hits = markerBook.filter((item) => item.route === route);
  if (hits.length !== 1) {
    throw new Error(`census marker book for ${route}: ${hits.length}`);
  }
  return [...hits[0].markers];
}

function pageFile(route: string): string {
  const folder = route === "/" ? appDir : path.join(appDir, ...route.slice(1).split("/"));
  const found = pageExtensions
    .map((ext) => path.join(folder, `page.${ext}`))
    .filter((file) => existsSync(file));
  if (found.length !== 1) {
    throw new Error(`page file for ${route}: expected 1, found ${found.length}`);
  }
  return found[0];
}

export function pageCensus(): PageCensus[] {
  const ledger = pageLedger();
  const routes = ledger.map((page) => page.route);
  if (markerBook.length !== routes.length || new Set(markerBook.map((item) => item.route)).size !== markerBook.length) {
    throw new Error("census marker book must list each ledger route once");
  }
  for (const item of markerBook) {
    if (!routes.includes(item.route)) {
      throw new Error(`census extra route ${item.route}`);
    }
  }

  return ledger.map((page) => {
    const abs = pageFile(page.route);
    const recorded = markersFor(page.route);
    const scanned = scanMarkers(readFileSync(abs, "utf8"));
    if (!sameMarkers(recorded, scanned)) {
      const left = recorded.join(",") || "<none>";
      const right = scanned.join(",") || "<none>";
      throw new Error(`census markers drifted for ${page.route}: recorded ${left} scanned ${right}`);
    }
    const surface = classifySurface(page.route, page.id);
    return {
      route: page.route,
      id: page.id,
      file: path.relative(webRoot, abs).split(path.sep).join("/"),
      surface,
      stack: "next",
      markers: recorded,
      token_source: "not_measured",
      states: "not_measured",
      responsive: "not_measured",
      accessibility: "not_measured",
      screenshot: "not_measured",
    };
  });
}
