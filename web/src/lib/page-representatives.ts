// HUI-2620 第三片：只从已有页面普查里记 6 个代表页。
// 输出顺序固定：Entry、Home、List、Detail、Main Action、Result。
// 每一类按普查现有顺序取第一条，大小写敏感。
// Entry 是 portal 或 login。Result 先 media_result，没有再 review。
// 缺席不补路由。screenshot 永远是 not_measured。不改页面。
// 这一片不能标 Done，也不关闭 HUI-2619、HUI-2628、HUI-2748。

import { pageCensus } from "./page-census";

export interface PageRepresentative {
  route: string;
  file: string;
  id: string;
  surface: string;
  stack: string;
  screenshot: "not_measured";
}

type CensusSlice = {
  route: string;
  file: string;
  id: string;
  surface: string;
  stack: string;
};

function absent(): PageRepresentative {
  return {
    route: "not_present",
    file: "not_present",
    id: "not_present",
    surface: "not_present",
    stack: "not_present",
    screenshot: "not_measured",
  };
}

function present(row: CensusSlice): PageRepresentative {
  return {
    route: row.route,
    file: row.file,
    id: row.id,
    surface: row.surface,
    stack: row.stack,
    screenshot: "not_measured",
  };
}

function firstId(rows: readonly CensusSlice[], ids: readonly string[]): CensusSlice | undefined {
  return rows.find((row) => ids.includes(row.id));
}

function taken(row: CensusSlice | undefined): PageRepresentative {
  return row === undefined ? absent() : present(row);
}

export function selectRepresentatives(rows: readonly CensusSlice[]): PageRepresentative[] {
  const result = firstId(rows, ["media_result"]) ?? firstId(rows, ["review"]);
  return [
    taken(firstId(rows, ["portal", "login"])),
    taken(firstId(rows, ["workspace"])),
    taken(firstId(rows, ["list"])),
    taken(firstId(rows, ["detail"])),
    taken(firstId(rows, ["creation"])),
    taken(result),
  ];
}

export function pageRepresentatives(): PageRepresentative[] {
  return selectRepresentatives(pageCensus());
}
