# blind family 材料（HUI-2628 r3，供 HUI-2625 blind family review）

## 性质

只产出材料，不产出结论。**verdict: not_run**（盲评由 HUI-2625 Gate 所有者执行，本票无权代填）。

## 材料

1440 视口、商家四代表页，EcoTopNav 品牌区屏蔽后的截图：

| 文件 | 页面 |
|---|---|
| blind-home.png | /（TodayDesk 商家首屏） |
| blind-work-stores.png | /work/stores |
| blind-work-campaign-detail.png | /work/campaigns/[activeId]（seed.json） |
| blind-work-rewards.png | /work/rewards |

## 屏蔽位

`web/e2e/specs/08-blind-family-materials.spec.ts` 的 MASK_STYLE，`visibility:hidden`：

- `[data-testid="eco-brand"]`（品牌名/Logo）
- `[data-testid="eco-current-app"]`（当前应用）
- `[data-testid="eco-payer"]`（付款主体需确认）
- `[data-testid="eco-provenance"]`（额度需确认）
- `[data-testid="eco-agent-banner"]`（代理来源横幅）
- `[data-testid^="eco-tenant-"]`（租户标识）
- `[data-testid="eco-top-nav"] button[aria-label^="账户"]`（右侧账户昵称）

页面正文、布局、组件、token 均未改动——盲评对象即真实产品皮肤，仅去品牌。
