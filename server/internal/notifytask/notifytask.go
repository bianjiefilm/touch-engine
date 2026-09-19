// Package notifytask holds the T0 client scaffolding for platform-notify and
// platform-task. Per ticket HUI-1746 these are CONFIG + SWITCH ONLY: no real
// events are produced or consumed in T0. Every call with the feature off (the
// default) or with missing configuration returns an explicit error — there is
// no fake-success path.
//
// 契约对齐 docs/integration-guide.md:notify 监听 18105,请求须带
// X-PilotSeaView-Internal-Token + X-Notify-App-ID;task 监听 18103,
// 头 X-PilotSeaView-Internal-Token + X-App-ID;凭证 = 每 app × 每服务一把。
package notifytask

import "errors"

// ErrFeatureDisabled is returned when the FEATURE_* switch is off (default).
var ErrFeatureDisabled = errors.New("notifytask: feature disabled by configuration (FEATURE_*=off)")

// ErrNotConfigured is returned when the switch is on but base URL or token is
// missing. This is a configuration problem and must fail closed.
var ErrNotConfigured = errors.New("notifytask: feature enabled but platform endpoint/token not configured")

// Side names the platform service a scaffold points at.
type Side string

const (
	SideNotify Side = "notify"
	SideTask   Side = "task"
)

// Scaffold is the inert client scaffold for one platform side.
type Scaffold struct {
	Side      Side
	Enabled   bool
	BaseURL   string
	Token     string
	AppID     string
	AskHeader string // extra app header required by the service (X-Notify-App-ID for notify)
}

// New builds a scaffold from resolved config values.
func New(side Side, enabled bool, baseURL, token, appID, askHeader string) *Scaffold {
	return &Scaffold{Side: side, Enabled: enabled, BaseURL: baseURL, Token: token, AppID: appID, AskHeader: askHeader}
}

// Check validates the scaffold for actual use. Feature off -> ErrFeatureDisabled;
// feature on with missing endpoint/token -> ErrNotConfigured. T0 has no caller
// of Check outside tests: it exists so FEAT tickets inherit a fail-closed
// starting point instead of inventing one.
func (s *Scaffold) Check() error {
	if !s.Enabled {
		return ErrFeatureDisabled
	}
	if s.BaseURL == "" || s.Token == "" || s.AppID == "" {
		return ErrNotConfigured
	}
	return nil
}
