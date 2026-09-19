-- HUI-1665 FEAT-0166: NFC 标签管理(additive:T0/T1 表零改动)。
-- 纪律:
--   - 标签是既有 campaign_link(短码)的管理面包装,不是第二套链接模型:
--     路由唯一事实来源仍是 campaign_links(五态解析零改动)。
--   - uid_hint 是物理写入后人工回填的标签 UID 提示(非凭证、不参与路由、
--     不进任何公共载荷);可空。
--   - 停用语义:标签停用时由应用层(同事务)把其绑定 link 置为停用,
--     短码路由复用既有五态停用态;本表 status 只记录管理面意图。

PRAGMA foreign_keys = ON;

-- 标签分组:纯租户内命名分组。
CREATE TABLE IF NOT EXISTS nfc_tag_groups (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    name       TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (tenant_id, name)
);

-- NFC 标签:label 为操作员可读名;store_id/group_id 可空(未绑/未分组)。
CREATE TABLE IF NOT EXISTS nfc_tags (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    group_id   TEXT REFERENCES nfc_tag_groups(id),
    store_id   TEXT REFERENCES stores(id),
    link_id    TEXT NOT NULL REFERENCES campaign_links(id),
    uid_hint   TEXT,
    label      TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_nfc_tags_tenant ON nfc_tags(tenant_id);
CREATE INDEX IF NOT EXISTS idx_nfc_tags_group ON nfc_tags(group_id);
CREATE INDEX IF NOT EXISTS idx_nfc_tags_link ON nfc_tags(link_id);
CREATE INDEX IF NOT EXISTS idx_nfc_tag_groups_tenant ON nfc_tag_groups(tenant_id);
