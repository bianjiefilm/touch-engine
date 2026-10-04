-- HUI-2748 r2: merchant-declared placement channels and aspect ratios.
-- 这两个字段是商家声明的投放渠道/画幅比例，属于 Handoff Brief 的输入，
-- 不是 Motion 状态。additive only：不改 0018 锁死的任何 CHECK。
-- 格式约束（逗号分隔小写 token）在 storemotion 包校验，库里只存文本。

ALTER TABLE store_motion_requests ADD COLUMN channels TEXT NOT NULL DEFAULT '';
ALTER TABLE store_motion_requests ADD COLUMN aspect_ratios TEXT NOT NULL DEFAULT '';
