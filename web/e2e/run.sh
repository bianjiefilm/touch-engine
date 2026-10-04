#!/usr/bin/env bash
# HUI-2628 r2 E2E 一键运行（抄 guanlan-order/web/harness/run.sh 自含模式）。
# 用法：bash web/e2e/run.sh [playwright 额外参数]
#   带过滤参数（如 06-a11y）为 partial 模式：证据门只查 skip/失败，不查 17 张截图全集；
#   不带参数为验收全量模式：证据门全开（截图全集 + axe blocking=0）。
# 前置：web/ 已 npm ci；web/e2e/ 已 npm install；npm run build 已随 env up 前完成由本脚本执行。
set -u
cd "$(dirname "$0")/.."   # web/（本脚本在 web/e2e/ 下，上跳一级即 web/）

echo "[run] next build"
npm run build || exit 1

node e2e/scripts/env.mjs up
UP=$?
if [ $UP -ne 0 ]; then
  node e2e/scripts/env.mjs down
  echo "[run] 环境拉起失败（exit ${UP}）——按 FAIL 处理"
  exit $UP
fi

# 用 e2e 本地二进制，不用 npx——机器上有全局 playwright（版本不同）会被 npx 优先选中，
# runner 与 specs import 的 @playwright/test 版本分裂会报 "test() called here"。
e2e/node_modules/.bin/playwright test -c e2e/playwright.config.ts "$@"
PW=$?

node e2e/scripts/env.mjs down

echo "[run] playwright exit=$PW"
# 证据汇总门（评审 A1）：即使 playwright exit 0，也要解析 JSON 报告查 skip/失败；
# 全量模式再查 17 张验收截图与 axe blocking，防证据缺失的假绿。
GATE_ARGS=--partial
[ $# -eq 0 ] && GATE_ARGS=
node e2e/scripts/evidence-gate.mjs $GATE_ARGS
GATE=$?

# 验收门：playwright 非 0 即失败；证据门非 0（含 skipped>0 / 截图缺失）也失败。
if [ $PW -ne 0 ] || [ $GATE -ne 0 ]; then
  echo "[run] FAIL（playwright=${PW}，evidence-gate=${GATE}）"
  exit 1
fi
echo "[run] PASS"
exit 0
