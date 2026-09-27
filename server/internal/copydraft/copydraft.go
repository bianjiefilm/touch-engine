// Package copydraft builds campaign copy drafts from a confirmed snapshot.
//
// HUI-1668: without a real model authorization the result keeps the draft
// structure and the gaps, and it is never labeled usable. Expired prices,
// uncertain addresses or hours, and claims without evidence are not copied
// into title, intro, topics, or the professional-tool handoff. A list of
// place names is not a platform POI mount. This package does not bill.
package copydraft

import "strings"

const (
	StatusConfirmed = "confirmed"
	StatusExpired   = "expired"
	StatusUncertain = "uncertain"
	StatusAbsent    = "absent"

	ModelNotAuthorized = "not_authorized"
	ModelAuthorized    = "authorized"
)

// Fact is one merchant-supplied field and how sure they are.
type Fact struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// Claim is a marketing sentence. Evidence empty means it cannot be stated.
type Claim struct {
	Text     string `json:"text"`
	Evidence string `json:"evidence"`
}

// Input is the snapshot for one generation. ChannelMountAvailable is decided
// by the server from a real channel action, never from the request flag alone.
type Input struct {
	StoreName             string
	CampaignTitle         string
	PublicContent         string
	Price                 Fact
	Address               Fact
	Hours                 Fact
	Claims                []Claim
	POINames              []string
	ChannelMountAvailable bool
	AssetIDs              []string
	ModelAuthorized       bool
}

// ModelOutput is text returned by an authorized model call.
type ModelOutput struct {
	Title  string
	Intro  string
	Topics []string
}

// Gap asks the merchant to supply a missing or stale fact.
type Gap struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// POI separates place-name suggestions from an actual platform mount.
type POI struct {
	Suggestions []string `json:"suggestions"`
	Mounted     bool     `json:"mounted"`
	Message     string   `json:"message"`
}

// Handoff is the confirmed snapshot a professional tool can reuse.
// It carries no launch URL.
type Handoff struct {
	StoreName       string   `json:"store_name,omitempty"`
	CampaignTitle   string   `json:"campaign_title,omitempty"`
	PublicContent   string   `json:"public_content,omitempty"`
	Price           string   `json:"price,omitempty"`
	Address         string   `json:"address,omitempty"`
	Hours           string   `json:"hours,omitempty"`
	AssetIDs        []string `json:"asset_ids"`
	EvidencedClaims []string `json:"evidenced_claims,omitempty"`
}

// Draft is a copy candidate. Usable is true only after an authorized model
// returned a title that survived the fact check.
type Draft struct {
	Usable         bool     `json:"usable"`
	ModelStatus    string   `json:"model_status"`
	Billed         bool     `json:"billed"`
	Title          string   `json:"title"`
	Intro          string   `json:"intro"`
	Topics         []string `json:"topics"`
	Notice         string   `json:"notice"`
	Gaps           []Gap    `json:"gaps"`
	ConfirmedFacts []string `json:"confirmed_facts"`
	POI            POI      `json:"poi"`
	Handoff        Handoff  `json:"professional_handoff"`
}

// Compose builds the unauthorized (or pre-model) draft. Copy fields stay empty.
func Compose(in Input) Draft {
	d := Draft{
		Usable:      false,
		ModelStatus: ModelNotAuthorized,
		Billed:      false,
		Title:       "",
		Intro:       "",
		Topics:      []string{},
		Notice:      "模型未授权。这里只保留已确认资料和待补充项，不是真实可用文案。",
		Gaps:        gapsFor(in),
		Handoff: Handoff{
			AssetIDs: nonNil(in.AssetIDs),
		},
	}
	if s := strings.TrimSpace(in.StoreName); s != "" {
		d.ConfirmedFacts = append(d.ConfirmedFacts, "门店名:"+s)
		d.Handoff.StoreName = s
	}
	if s := strings.TrimSpace(in.CampaignTitle); s != "" {
		d.ConfirmedFacts = append(d.ConfirmedFacts, "活动标题:"+s)
		d.Handoff.CampaignTitle = s
	}
	if s := strings.TrimSpace(in.PublicContent); s != "" {
		d.ConfirmedFacts = append(d.ConfirmedFacts, "公开内容:"+s)
		d.Handoff.PublicContent = s
	}
	if confirmed(in.Price) {
		d.ConfirmedFacts = append(d.ConfirmedFacts, "价格:"+strings.TrimSpace(in.Price.Text))
		d.Handoff.Price = strings.TrimSpace(in.Price.Text)
	}
	if confirmed(in.Address) {
		d.ConfirmedFacts = append(d.ConfirmedFacts, "地址:"+strings.TrimSpace(in.Address.Text))
		d.Handoff.Address = strings.TrimSpace(in.Address.Text)
	}
	if confirmed(in.Hours) {
		d.ConfirmedFacts = append(d.ConfirmedFacts, "营业时间:"+strings.TrimSpace(in.Hours.Text))
		d.Handoff.Hours = strings.TrimSpace(in.Hours.Text)
	}
	for _, c := range in.Claims {
		if strings.TrimSpace(c.Evidence) == "" || strings.TrimSpace(c.Text) == "" {
			continue
		}
		text := strings.TrimSpace(c.Text)
		d.ConfirmedFacts = append(d.ConfirmedFacts, "宣称:"+text)
		d.Handoff.EvidencedClaims = append(d.Handoff.EvidencedClaims, text)
	}
	d.POI = poiFor(in)
	if d.ConfirmedFacts == nil {
		d.ConfirmedFacts = []string{}
	}
	return d
}

// ApplyModel keeps Compose when the model is not authorized. An authorized
// result may fill title, intro, and topics only with sentences that do not
// repeat unconfirmed facts. Nothing here captures a payment.
func ApplyModel(in Input, out ModelOutput) Draft {
	d := Compose(in)
	if !in.ModelAuthorized {
		return d
	}
	withheld := withheldTexts(in)
	confirmedTexts := confirmedCorpus(in)
	dropped := false
	if title, ok := keep(out.Title, withheld, confirmedTexts); ok {
		d.Title = title
	} else if strings.TrimSpace(out.Title) != "" {
		dropped = true
	}
	if intro, ok := keep(out.Intro, withheld, confirmedTexts); ok {
		d.Intro = intro
	} else if strings.TrimSpace(out.Intro) != "" {
		dropped = true
	}
	topics := make([]string, 0, len(out.Topics))
	for _, topic := range out.Topics {
		cleaned, ok := keep(topic, withheld, confirmedTexts)
		if !ok {
			if strings.TrimSpace(topic) != "" {
				dropped = true
			}
			continue
		}
		topics = append(topics, cleaned)
	}
	d.Topics = topics
	d.ModelStatus = ModelAuthorized
	d.Billed = false
	d.Usable = d.Title != ""
	if dropped {
		d.Gaps = append(d.Gaps, Gap{
			Code:    "model_output_withheld",
			Message: "模型输出含有未确认事实，相关句子已整段去掉。请补充资料后再生成。",
		})
	}
	if d.Usable {
		d.Notice = "模型输出已按已确认资料核对。这仍是草稿，尚未发布，也没有扣费。"
	} else {
		d.Notice = "模型输出未能留下可使用的标题。这不是真实可用文案，也没有扣费。"
	}
	return d
}

func gapsFor(in Input) []Gap {
	var gaps []Gap
	switch {
	case in.Price.Status == StatusExpired:
		gaps = append(gaps, Gap{Code: "price_expired", Message: "价格已过期，请补充当前价格。系统不会把过期价格写成文案。"})
	case !confirmed(in.Price):
		gaps = append(gaps, Gap{Code: "price_missing", Message: "没有已确认的价格，请补充。系统不会编造价格。"})
	}
	if !confirmed(in.Address) {
		gaps = append(gaps, Gap{Code: "address_uncertain", Message: "地址不确定或与门店记录不一致，请补充。系统不会把未确认地址写成事实。"})
	}
	if !confirmed(in.Hours) {
		gaps = append(gaps, Gap{Code: "hours_uncertain", Message: "营业时间不确定，请补充。系统不会编造营业时间。"})
	}
	for _, c := range in.Claims {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		if strings.TrimSpace(c.Evidence) == "" {
			gaps = append(gaps, Gap{Code: "claim_without_evidence", Message: "营销宣称缺少证据，请补充依据。系统不会把该宣称写成事实。"})
		}
	}
	if gaps == nil {
		gaps = []Gap{}
	}
	return gaps
}

func poiFor(in Input) POI {
	var names []string
	for _, n := range in.POINames {
		n = strings.TrimSpace(n)
		if n != "" {
			names = append(names, n)
		}
	}
	if names == nil {
		names = []string{}
	}
	if in.ChannelMountAvailable && len(names) > 0 {
		return POI{Suggestions: names, Mounted: true, Message: "渠道动作可用，这些地点可挂载。地点名本身不是绑定成功。"}
	}
	if len(names) > 0 {
		return POI{Suggestions: names, Mounted: false, Message: "这些地点名只是文字建议，没有绑定到平台 POI。"}
	}
	return POI{Suggestions: names, Mounted: false, Message: "没有地点建议，也没有绑定平台 POI。"}
}

func confirmed(f Fact) bool {
	return f.Status == StatusConfirmed && strings.TrimSpace(f.Text) != ""
}

func withheldTexts(in Input) []string {
	var out []string
	if !confirmed(in.Price) {
		out = append(out, in.Price.Text)
	}
	if !confirmed(in.Address) {
		out = append(out, in.Address.Text)
	}
	if !confirmed(in.Hours) {
		out = append(out, in.Hours.Text)
	}
	for _, c := range in.Claims {
		if strings.TrimSpace(c.Evidence) == "" {
			out = append(out, c.Text)
		}
	}
	return out
}

func confirmedCorpus(in Input) []string {
	var out []string
	out = append(out, in.StoreName, in.CampaignTitle, in.PublicContent)
	if confirmed(in.Price) {
		out = append(out, in.Price.Text)
	}
	if confirmed(in.Address) {
		out = append(out, in.Address.Text)
	}
	if confirmed(in.Hours) {
		out = append(out, in.Hours.Text)
	}
	for _, c := range in.Claims {
		if strings.TrimSpace(c.Evidence) != "" {
			out = append(out, c.Text)
		}
	}
	return out
}

func keep(field string, withheld, confirmed []string) (string, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return "", false
	}
	if leaks(field, withheld, confirmed) {
		return "", false
	}
	return field, true
}

func leaks(field string, withheld, confirmed []string) bool {
	for _, w := range withheld {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		rs := []rune(w)
		if len(rs) < 4 {
			if strings.Contains(field, w) && !covered(w, confirmed) {
				return true
			}
			continue
		}
		for n := 4; n <= len(rs); n++ {
			for i := 0; i+n <= len(rs); i++ {
				frag := string(rs[i : i+n])
				if strings.Contains(field, frag) && !covered(frag, confirmed) {
					return true
				}
			}
		}
	}
	return false
}

func covered(frag string, confirmed []string) bool {
	frag = strings.TrimSpace(frag)
	if frag == "" {
		return true
	}
	for _, c := range confirmed {
		if strings.Contains(c, frag) {
			return true
		}
	}
	return false
}

func nonNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
