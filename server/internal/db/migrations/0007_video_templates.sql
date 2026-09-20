-- HUI-1669 FEAT-0170: 视频模板管理(素材引用集的组织单元,零物理存储)。
-- Additive only:既有表零改动。纪律:
--   - 模板 = 具名结构:名称租户内唯一;模板内容 = 槽位定义列表 + 槽位到
--     FEAT-0167 素材库(lib_assets)的绑定;不建任何物理文件存储;
--   - 生命周期:draft → published;发布后该版本行只读(应用层强制,发布后
--     零 UPDATE);结构变更 = 产生新版本行(版本号只增,UNIQUE(template_id,
--     version));版本行永不删除;
--   - 既有分配钉住 (template_id, version):版本冻结语义与素材库选择台账同一
--     范式 —— 发布新版本绝不自动升级存量分配;version 无外键到版本行的
--     「发布状态」(状态在应用层判定),但模板/门店行有外键;
--   - 分配 = 模板⇄门店关系:一店可配多模板、一模板可配多店;
--     UNIQUE(store_id, template_id);留痕 = 行内 assigned_by/assigned_at/
--     version(+ 再分配的 updated_by/updated_at),可回查;
--   - 槽位内容(含绑定)以 JSON 存 slots 列:结构校验在应用层
--     (videotpl.ValidateContent),媒体类型/语义标签枚举不进 CHECK
--     (披露枚举由应用层统一把关,与 tags 列同一处理方式);
--   - 视频合成/渲染执行不做(属外部工具/AiCut):本库零生成/渲染字段。

PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS video_templates (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    name       TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_video_templates_tenant ON video_templates(tenant_id, created_at);

CREATE TABLE IF NOT EXISTS video_template_versions (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    template_id  TEXT NOT NULL REFERENCES video_templates(id),
    version      INTEGER NOT NULL CHECK (version >= 1),
    status       TEXT NOT NULL CHECK (status IN ('draft','published')),
    slots        TEXT NOT NULL DEFAULT '',
    created_by   TEXT NOT NULL DEFAULT '',
    published_by TEXT NOT NULL DEFAULT '',
    published_at TEXT,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (template_id, version)
);

CREATE INDEX IF NOT EXISTS idx_video_template_versions_tpl ON video_template_versions(template_id, version);

CREATE TABLE IF NOT EXISTS video_template_assignments (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    template_id TEXT NOT NULL REFERENCES video_templates(id),
    version     INTEGER NOT NULL CHECK (version >= 1),
    store_id    TEXT NOT NULL REFERENCES stores(id),
    assigned_by TEXT NOT NULL DEFAULT '',
    assigned_at TEXT NOT NULL,
    updated_by  TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL,
    UNIQUE (store_id, template_id)
);

CREATE INDEX IF NOT EXISTS idx_video_template_assignments_store ON video_template_assignments(tenant_id, store_id, assigned_at);
CREATE INDEX IF NOT EXISTS idx_video_template_assignments_tpl ON video_template_assignments(tenant_id, template_id, assigned_at);
