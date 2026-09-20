// Package config loads touch-engine server configuration from environment.
//
// 纪律:本进程只有 platform 一条身份路径(无 stub 模式);缺配置时服务可以
// 启动(便于健康探针与运维观察),但一切鉴权动作必须 fail-closed 显式报错,
// 绝不伪造成功。配置键名对齐 public-ai services checklist(HUI-1723 inventory §4)。
package config

import (
	"fmt"
	"os"
	"strings"
)

// Feature flag environment keys (default: off).
const (
	EnvFeatureNotify       = "FEATURE_NOTIFY"
	EnvFeatureUpload       = "FEATURE_UPLOAD"
	EnvFeatureTask         = "FEATURE_TASK"
	EnvFeatureLeadsCapture = "FEATURE_LEADS_CAPTURE"
	EnvFeatureDashboard    = "FEATURE_DASHBOARD"
	// EnvFeatureCampaignRules (HUI-1676 FEAT-0177): 登记制开关,默认 off。
	// off = 规则路由不注册(404)且频控不生效(现行为逐字节不变)。
	EnvFeatureCampaignRules = "FEATURE_CAMPAIGN_RULES"
	// EnvFeatureAssetLib (HUI-1666 FEAT-0167): 登记制开关,默认 off。
	// off = 素材/池/调取路由不注册(404 不可见)。零额外 Gate 项:素材行是
	// 引用记录,引用校验复用 FEATURE_UPLOAD 的平台客户端。
	EnvFeatureAssetLib = "FEATURE_ASSET_LIB"
	// EnvFeatureVideoTemplates (HUI-1669 FEAT-0170): 登记制开关,默认 off。
	// off = 视频模板全路由不注册(404 不可见)。零额外 Gate 项:模板是素材
	// 引用集的组织单元(零物理存储),绑定校验只读 assetlib 既有行。
	EnvFeatureVideoTemplates = "FEATURE_VIDEO_TEMPLATES"
)

// Config is the resolved server configuration.
type Config struct {
	// HTTPAddr is the loopback listen address, e.g. 127.0.0.1:18240.
	HTTPAddr string
	// DBPath is the sqlite database file path.
	DBPath string
	// Env is "development" or "production".
	Env string
	// AppID is the platform app id (JWT aud), e.g. touch-engine.
	AppID string
	// InternalToken is the shared secret between the web BFF and this server.
	InternalToken string
	// SessionCookie is the cookie name carrying the identity session token.
	SessionCookie string

	// Identity (platform-identity, loopback).
	IdentityBaseURL string
	IdentityToken   string
	IdentityAppHost string

	// Upload / Notify / Task clients. Upload validates material references in
	// T0 when FEATURE_UPLOAD=on; Notify/Task are scaffolding only.
	UploadBaseURL string
	UploadToken   string
	NotifyBaseURL string
	NotifyToken   string
	TaskBaseURL   string
	TaskToken     string

	// Lead capture (HUI-1747): delivery goes through platform-notify directed
	// events. The target acquisition app and the phone-fingerprint pepper are
	// dedicated, fail-closed configuration — never derived from request input.
	LeadsTargetApp   string // LEADS_TARGET_APP_ID (e.g. the CRM/acquisition app id)
	LeadsPhonePepper string // LEADS_PHONE_PEPPER (server-side HMAC pepper for phone fingerprints)

	// PublicBaseURL is the H5 origin guests reach (QR payload base, HUI-1664),
	// e.g. https://h5.example.com. Used ONLY to build the canonical short-code
	// URL <base>/c/<code>; never taken from request input. QR endpoints fail
	// closed (503 qr_not_configured) when it is unset.
	PublicBaseURL string

	FeatureUpload         bool
	FeatureNotify         bool
	FeatureTask           bool
	FeatureLeadsCapture   bool
	FeatureDashboard      bool
	FeatureCampaignRules  bool
	FeatureAssetLib       bool
	FeatureVideoTemplates bool
}

// FromEnv reads configuration from the process environment.
func FromEnv() Config {
	return Load(os.Getenv)
}

// Load builds a Config from any key->value getter (injection point for tests
// and for alternate config sources).
func Load(get func(string) string) Config {
	return Config{
		HTTPAddr:              firstNonEmpty(get("TOUCH_HTTP_ADDR"), "127.0.0.1:18240"),
		DBPath:                firstNonEmpty(get("TOUCH_DB_PATH"), "data/touch.db"),
		Env:                   firstNonEmpty(get("TOUCH_ENV"), "development"),
		AppID:                 firstNonEmpty(get("TOUCH_APP_ID"), "touch-engine"),
		InternalToken:         get("TOUCH_INTERNAL_TOKEN"),
		SessionCookie:         firstNonEmpty(get("TOUCH_SESSION_COOKIE"), "touch_session"),
		IdentityBaseURL:       get("PLATFORM_IDENTITY_BASE_URL"),
		IdentityToken:         get("PLATFORM_IDENTITY_TOKEN"),
		IdentityAppHost:       get("PLATFORM_IDENTITY_APP_HOST"),
		UploadBaseURL:         get("PLATFORM_UPLOAD_BASE_URL"),
		UploadToken:           get("PLATFORM_UPLOAD_TOKEN"),
		NotifyBaseURL:         get("PLATFORM_NOTIFY_BASE_URL"),
		NotifyToken:           get("PLATFORM_NOTIFY_TOKEN"),
		TaskBaseURL:           get("PLATFORM_TASK_BASE_URL"),
		TaskToken:             get("PLATFORM_TASK_TOKEN"),
		LeadsTargetApp:        get("LEADS_TARGET_APP_ID"),
		LeadsPhonePepper:      get("LEADS_PHONE_PEPPER"),
		PublicBaseURL:         strings.TrimSpace(get("PUBLIC_BASE_URL")),
		FeatureUpload:         isTruthy(get(EnvFeatureUpload)),
		FeatureNotify:         isTruthy(get(EnvFeatureNotify)),
		FeatureTask:           isTruthy(get(EnvFeatureTask)),
		FeatureLeadsCapture:   isTruthy(get(EnvFeatureLeadsCapture)),
		FeatureDashboard:      isTruthy(get(EnvFeatureDashboard)),
		FeatureCampaignRules:  isTruthy(get(EnvFeatureCampaignRules)),
		FeatureAssetLib:       isTruthy(get(EnvFeatureAssetLib)),
		FeatureVideoTemplates: isTruthy(get(EnvFeatureVideoTemplates)),
	}
}

// Gate returns the list of configuration problems that must fail closed.
// An empty list means every authenticated path is fully configured.
func (c Config) Gate() []string {
	var problems []string
	if strings.TrimSpace(c.InternalToken) == "" {
		problems = append(problems, "TOUCH_INTERNAL_TOKEN is required (web BFF -> server shared secret)")
	}
	if strings.TrimSpace(c.IdentityBaseURL) == "" {
		problems = append(problems, "PLATFORM_IDENTITY_BASE_URL is required (platform-identity loopback url)")
	}
	if strings.TrimSpace(c.IdentityToken) == "" {
		problems = append(problems, "PLATFORM_IDENTITY_TOKEN is required (this app's dedicated identity token; never a shared master token)")
	}
	if c.FeatureUpload {
		if strings.TrimSpace(c.UploadBaseURL) == "" {
			problems = append(problems, "FEATURE_UPLOAD=on requires PLATFORM_UPLOAD_BASE_URL")
		}
		if strings.TrimSpace(c.UploadToken) == "" {
			problems = append(problems, "FEATURE_UPLOAD=on requires PLATFORM_UPLOAD_TOKEN (platform-side UPLOAD_APP_TOKENS entry)")
		}
	}
	if c.FeatureNotify {
		if strings.TrimSpace(c.NotifyBaseURL) == "" {
			problems = append(problems, "FEATURE_NOTIFY=on requires PLATFORM_NOTIFY_BASE_URL")
		}
		if strings.TrimSpace(c.NotifyToken) == "" {
			problems = append(problems, "FEATURE_NOTIFY=on requires PLATFORM_NOTIFY_TOKEN")
		}
	}
	if c.FeatureTask {
		if strings.TrimSpace(c.TaskBaseURL) == "" {
			problems = append(problems, "FEATURE_TASK=on requires PLATFORM_TASK_BASE_URL")
		}
		if strings.TrimSpace(c.TaskToken) == "" {
			problems = append(problems, "PLATFORM_TASK_TOKEN is required when FEATURE_TASK=on")
		}
	}
	if c.FeatureLeadsCapture {
		if strings.TrimSpace(c.NotifyBaseURL) == "" {
			problems = append(problems, "FEATURE_LEADS_CAPTURE=on requires PLATFORM_NOTIFY_BASE_URL (directed-event delivery)")
		}
		if strings.TrimSpace(c.NotifyToken) == "" {
			problems = append(problems, "FEATURE_LEADS_CAPTURE=on requires PLATFORM_NOTIFY_TOKEN")
		}
		if strings.TrimSpace(c.LeadsTargetApp) == "" {
			problems = append(problems, "FEATURE_LEADS_CAPTURE=on requires LEADS_TARGET_APP_ID (acquisition/CRM app the lead events are directed to)")
		}
		if strings.TrimSpace(c.LeadsPhonePepper) == "" {
			problems = append(problems, "FEATURE_LEADS_CAPTURE=on requires LEADS_PHONE_PEPPER (server-side phone-fingerprint pepper; never commit a real value)")
		}
	}
	return problems
}

// Production reports whether the process runs with production discipline.
func (c Config) Production() bool { return strings.EqualFold(strings.TrimSpace(c.Env), "production") }

func (c Config) Describe() string {
	flags := fmt.Sprintf("upload=%v,notify=%v,task=%v,leads=%v,dashboard=%v", c.FeatureUpload, c.FeatureNotify, c.FeatureTask, c.FeatureLeadsCapture, c.FeatureDashboard)
	return fmt.Sprintf("env=%s app_id=%s addr=%s db=%s features(%s)",
		c.Env, c.AppID, c.HTTPAddr, c.DBPath, flags)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
