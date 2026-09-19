-- HUI-1747 T1: 活动授权留资(lead capture)→ 获客定向投递(E4 directed-event/v1)。
-- Additive only:T0 表零改动。纪律:
--   - 联系方式(姓名/手机号/微信号)只存 lead_submissions(授权源域本地表),
--     绝不出现在事件载荷/日志/URL;事件只携带 submission_ref + consent/provenance 引用。
--   - leads_outbox 与 lead_submissions 由应用层保证同事务写入(原子提交契约)。
--   - target_tenant 不存在任何输入路径:租户由服务端取活动归属。

PRAGMA foreign_keys = ON;

-- 留资表单挂载点:每活动一行固定轻表单配置(可视化搭建归 HUI-1679 后续票)。
-- notice_version 记录告知文案版本,消费者同意时随提交落 lead_submissions.consent_notice_version。
CREATE TABLE IF NOT EXISTS lead_forms (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL REFERENCES tenants(id),
    campaign_id             TEXT NOT NULL REFERENCES campaigns(id),
    notice_version          TEXT NOT NULL DEFAULT 'v1',
    marketing_optin_enabled INTEGER NOT NULL DEFAULT 1 CHECK (marketing_optin_enabled IN (0,1)),
    enabled                 INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    created_by              TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    UNIQUE (campaign_id)
);

-- 线索(授权源域):sync_state 状态机
--   accepted(本地已接受) -> pending_sync(已交 notify,待 CRM) -> crm_received(CRM 已接收)
--   任意活动态 -> rejected(目标拒绝/死信) | revoked(已撤销,终态不可逆)
CREATE TABLE IF NOT EXISTS lead_submissions (
    id                     TEXT PRIMARY KEY,
    tenant_id              TEXT NOT NULL REFERENCES tenants(id),
    campaign_id            TEXT NOT NULL REFERENCES campaigns(id),
    store_id               TEXT NOT NULL DEFAULT '',
    link_id                TEXT NOT NULL DEFAULT '',
    form_id                TEXT NOT NULL DEFAULT '',
    submission_ref         TEXT NOT NULL UNIQUE,
    dedup_key              TEXT NOT NULL UNIQUE,
    name                   TEXT NOT NULL DEFAULT '',
    phone                  TEXT NOT NULL DEFAULT '',
    wechat                 TEXT NOT NULL DEFAULT '',
    marketing_optin        INTEGER NOT NULL DEFAULT 0 CHECK (marketing_optin IN (0,1)),
    consent_notice_version TEXT NOT NULL DEFAULT '',
    consent_at             TEXT NOT NULL,
    consent_ip_fp          TEXT NOT NULL DEFAULT '',
    sync_state             TEXT NOT NULL DEFAULT 'accepted'
        CHECK (sync_state IN ('accepted','pending_sync','crm_received','rejected','revoked')),
    sync_error             TEXT NOT NULL DEFAULT '',
    attempts               INTEGER NOT NULL DEFAULT 0,
    source_version         INTEGER NOT NULL DEFAULT 1,
    revoked_at             TEXT,
    created_at             TEXT NOT NULL,
    updated_at             TEXT NOT NULL
);

-- 源 outbox:与线索行同事务写入的事件封套队列(转发器唯一事实来源)。
-- kind: submit(线索提交事实) | revoke(撤销/停止营销通知,PROVISIONAL 枚举扩展)。
-- forwarded: 0=待发, 1=已发(notify 已持久接受), 2=suppressed(撤销拦截,永不发送)。
CREATE TABLE IF NOT EXISTS leads_outbox (
    event_id       TEXT PRIMARY KEY,
    submission_ref TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('submit','revoke')),
    payload_json   TEXT NOT NULL,
    forwarded      INTEGER NOT NULL DEFAULT 0 CHECK (forwarded IN (0,1,2)),
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

-- 最小审计(无 PII):动作流水,撤销争议时可追溯。
CREATE TABLE IF NOT EXISTS lead_audit (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    submission_ref TEXT NOT NULL,
    action         TEXT NOT NULL,
    detail         TEXT NOT NULL DEFAULT '',
    actor          TEXT NOT NULL DEFAULT 'system',
    created_at     TEXT NOT NULL
);

-- 匿名浏览统计:纯聚合(码+日+渠道),没有任何身份/设备/网络标识列,
-- 结构上不可能汇入联系人池(线索与浏览统计分开)。
CREATE TABLE IF NOT EXISTS public_view_stats (
    code    TEXT NOT NULL,
    day     TEXT NOT NULL,
    channel TEXT NOT NULL DEFAULT '',
    views   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (code, day, channel)
);

CREATE INDEX IF NOT EXISTS idx_lead_forms_campaign ON lead_forms(campaign_id);
CREATE INDEX IF NOT EXISTS idx_lead_submissions_tenant ON lead_submissions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_lead_submissions_campaign ON lead_submissions(campaign_id);
CREATE INDEX IF NOT EXISTS idx_leads_outbox_pending ON leads_outbox(forwarded, created_at);
CREATE INDEX IF NOT EXISTS idx_lead_audit_ref ON lead_audit(submission_ref);
