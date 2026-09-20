// Package assetlib implements HUI-1666 FEAT-0167 商家素材库 v1: the pure,
// deterministic domain rules for asset registration, the product-photo/AiCut
// import path, and deterministic random pool selection.
//
// 纪律(票面拍板):
//   - v1 = 素材登记与组织面,不建物理文件仓库:素材行是对 platform-upload
//     产物(既有 upload 面的 asset_id)的引用记录;接口只收元数据+指纹,
//     大文件零进 Context/Notify;
//   - 授权语义 v1 = 引用级声明:来源 source、用途 purpose、授权声明 grant_ref
//     缺任一即拒绝(独立机器原因码);物理授权资产版本面(HUI-1732)未建,
//     grant_ref 是不透明声明引用,本包不解释其内容(deferred,如实声明);
//   - 素材引用必须显式登记,不存在任何通配/隐式资产访问;
//   - 调取 = 纯函数确定性随机:种子 = (pool_id, 当日 UTC 日期),同种子必同选,
//     可复现可测;调取不改变池(Select 是纯函数);
//   - 「回流候选」v1 = 素材行/选择行的候选标记:回流结果先入候选,不直接替换。
package assetlib

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ---- media type / source enumerations ---------------------------------------

// MediaType is the v1 media-type enumeration (票面固定三种).
type MediaType string

const (
	MediaTypeImage MediaType = "image" // 产品图/海报等静态图
	MediaTypeVideo MediaType = "video" // 成片/短视频
	MediaTypeBGM   MediaType = "bgm"   // 背景音乐
)

// Valid reports whether m is one of the three v1 media types.
func (m MediaType) Valid() bool {
	switch m {
	case MediaTypeImage, MediaTypeVideo, MediaTypeBGM:
		return true
	}
	return false
}

// Source is the declared provenance of an asset (引用级声明的「来源」).
type Source string

const (
	SourceProductPhoto   Source = "product_photo"   // 产品图
	SourceAiCut          Source = "aicut"           // AiCut 成片
	SourceMerchantUpload Source = "merchant_upload" // 商家自传(不可经导入路径)
)

// Valid reports whether s is one of the v1 sources.
func (s Source) Valid() bool {
	switch s {
	case SourceProductPhoto, SourceAiCut, SourceMerchantUpload:
		return true
	}
	return false
}

// Importable reports whether s may enter through the import endpoint
// (产品图/AiCut 导入已完成合法素材;其余来源必须走通用登记端点)。
func (s Source) Importable() bool {
	return s == SourceProductPhoto || s == SourceAiCut
}

// ---- machine reason codes ----------------------------------------------------

// Machine reason codes (written into 422 responses' error fields).
const (
	ReasonMissingAssetRef     = "missing_asset_ref"     // 引用标识缺失
	ReasonBadAssetRef         = "bad_asset_ref"         // 引用标识超长
	ReasonBadSha256           = "bad_sha256"            // sha256 非 64 位十六进制
	ReasonBadMediaType        = "bad_media_type"        // 媒体类型不在枚举内
	ReasonMissingSource       = "missing_source"        // 来源缺失
	ReasonBadSource           = "bad_source"            // 来源不在枚举内
	ReasonSourceNotImportable = "source_not_importable" // 该来源不可经导入路径
	ReasonMissingPurpose      = "missing_purpose"       // 用途缺失
	ReasonBadPurpose          = "bad_purpose"           // 用途超长
	ReasonMissingGrantRef     = "missing_grant_ref"     // 授权声明缺失
	ReasonBadGrantRef         = "bad_grant_ref"         // 授权声明超长
	ReasonBadTag              = "bad_tag"               // 使用位置标签非法
	ReasonMissingSelectKey    = "missing_select_key"    // 调取选择键缺失
	ReasonBadSelectKey        = "bad_select_key"        // 调取选择键超长
	ReasonMissingPoolName     = "missing_pool_name"     // 池名缺失
	ReasonBadPoolName         = "bad_pool_name"         // 池名超长
)

// ValidationError carries a machine-readable reason for the 422 mapping.
type ValidationError struct {
	Reason string
	Field  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("assetlib: %s (%s)", e.Reason, e.Field)
}

func invalid(reason, field string) error {
	return &ValidationError{Reason: reason, Field: field}
}

// NewValidationError is the exported constructor for the layered callers
// (store/httpapi build domain-typed failures without re-implementing codes).
func NewValidationError(reason, field string) error {
	return &ValidationError{Reason: reason, Field: field}
}

// ---- registration ------------------------------------------------------------

// Field length caps for the declaration fields (声明是短文本,不是内容体).
const (
	MaxAssetRef  = 256
	MaxPurpose   = 256
	MaxGrantRef  = 256
	MaxTagLen    = 64
	MaxTags      = 32
	MaxSelectKey = 128
	MaxPoolName  = 64
)

// Registration is the metadata-only registration payload. It never carries a
// file body: the fingerprint (SHA256) anchors the version at platform-upload.
type Registration struct {
	AssetRef  string    // platform upload asset_id(既有 upload 产物引用标识)
	SHA256    string    // 内容指纹 = 版本锚点(platform sha256)
	MediaType MediaType // image | video | bgm
	Source    Source    // product_photo | aicut | merchant_upload
	Purpose   string    // 用途声明(引用级,不解释)
	GrantRef  string    // 授权声明引用(HUI-1732 前为不透明字符串)
	Tags      []string  // 使用位置标签(可空)
	// Candidate marks a 回流候选 row: candidates are recorded BESIDE the
	// official row (回流结果先入候选,不直接替换). Declaration-only flag: it
	// changes nothing about the reference, the fingerprint, or any other field.
	Candidate bool
}

// NormalizeSHA256 canonicalizes the caller-supplied fingerprint.
func NormalizeSHA256(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NormalizeText trims the edges of a declaration field.
func NormalizeText(s string) string { return strings.TrimSpace(s) }

// NormalizeTags trims, drops empties and dedupes usage tags preserving order.
func NormalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ValidateRegistration enforces the declaration matrix: source/purpose/grant_ref
// are each mandatory (缺任一拒绝,独立原因码); the fingerprint must be a 64-hex
// sha256; media type and source must be in the v1 enumerations.
func ValidateRegistration(in Registration) error {
	in.SHA256 = NormalizeSHA256(in.SHA256)
	in.AssetRef = NormalizeText(in.AssetRef)
	in.Purpose = NormalizeText(in.Purpose)
	in.GrantRef = NormalizeText(in.GrantRef)
	mediaType := MediaType(NormalizeText(string(in.MediaType)))
	source := Source(NormalizeText(string(in.Source)))

	if in.AssetRef == "" {
		return invalid(ReasonMissingAssetRef, "asset_ref")
	}
	if len(in.AssetRef) > MaxAssetRef {
		return invalid(ReasonBadAssetRef, "asset_ref")
	}
	if !isSHA256Hex(in.SHA256) {
		return invalid(ReasonBadSha256, "sha256")
	}
	if !mediaType.Valid() {
		return invalid(ReasonBadMediaType, "media_type")
	}
	switch {
	case source == "":
		return invalid(ReasonMissingSource, "source")
	case !source.Valid():
		return invalid(ReasonBadSource, "source")
	}
	if in.Purpose == "" {
		return invalid(ReasonMissingPurpose, "purpose")
	}
	if len(in.Purpose) > MaxPurpose {
		return invalid(ReasonBadPurpose, "purpose")
	}
	if in.GrantRef == "" {
		return invalid(ReasonMissingGrantRef, "grant_ref")
	}
	if len(in.GrantRef) > MaxGrantRef {
		return invalid(ReasonBadGrantRef, "grant_ref")
	}
	tags := NormalizeTags(in.Tags)
	if len(tags) > MaxTags {
		return invalid(ReasonBadTag, "tags")
	}
	for _, t := range tags {
		if len(t) > MaxTagLen {
			return invalid(ReasonBadTag, "tags")
		}
	}
	return nil
}

// ValidateImport is ValidateRegistration plus the import-path source
// restriction: only 产品图/AiCut may enter through the import endpoint. The
// import path performs zero generation and zero model calls by construction —
// it validates a reference and records the declaration, nothing else.
func ValidateImport(in Registration) error {
	if err := ValidateRegistration(in); err != nil {
		return err
	}
	if !Source(NormalizeText(string(in.Source))).Importable() {
		return invalid(ReasonSourceNotImportable, "source")
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ValidateSelectKey checks the caller-supplied selection key (同键幂等的键半边;
// 种子本身只由 (pool_id, dayUTC) 派生,选择键绝不参与种子).
func ValidateSelectKey(key string) error {
	key = NormalizeText(key)
	switch {
	case key == "":
		return invalid(ReasonMissingSelectKey, "select_key")
	case len(key) > MaxSelectKey:
		return invalid(ReasonBadSelectKey, "select_key")
	}
	return nil
}

// ValidatePoolName checks a pool name (租户内唯一的显示名).
func ValidatePoolName(name string) error {
	name = NormalizeText(name)
	switch {
	case name == "":
		return invalid(ReasonMissingPoolName, "name")
	case len(name) > MaxPoolName:
		return invalid(ReasonBadPoolName, "name")
	}
	return nil
}

// ---- deterministic random selection ------------------------------------------

// ErrEmptyPool marks a pool调取 attempt against a pool with no items.
var ErrEmptyPool = errors.New("assetlib: empty pool")

// dayLayout is the UTC calendar-day seed format.
const dayLayout = "2006-01-02"

// NormalizeDayUTC folds any instant into its UTC calendar day (种子用当日
// UTC 日期,与时区无关).
func NormalizeDayUTC(t time.Time) string { return t.UTC().Format(dayLayout) }

// ValidDayUTC reports whether day is a YYYY-MM-DD calendar day.
func ValidDayUTC(day string) bool {
	_, err := time.Parse(dayLayout, day)
	return err == nil
}

// SelectionSeed derives the selection seed from exactly (pool_id, dayUTC):
// lowercase hex sha256 over the two values with a domain separator. Same seed
// must always yield the same pick (同种子必同选,可复现可测).
func SelectionSeed(poolID, dayUTC string) string {
	sum := sha256.Sum256([]byte("assetlib/selection\x00" + poolID + "\x00" + dayUTC))
	return hex.EncodeToString(sum[:])
}

// PickIndex maps the seed to an index in [0, n). n must be > 0; the caller
// checks emptiness (Select returns ErrEmptyPool for it). Pure function: no
// state, no clock, no randomness beyond the seed.
func PickIndex(poolID, dayUTC string, n int) int {
	if n <= 0 {
		panic("assetlib: PickIndex on empty pool")
	}
	sum := sha256.Sum256([]byte(SelectionSeed(poolID, dayUTC)))
	v := uint64(sum[0])<<56 | uint64(sum[1])<<48 | uint64(sum[2])<<40 | uint64(sum[3])<<32 |
		uint64(sum[4])<<24 | uint64(sum[5])<<16 | uint64(sum[6])<<8 | uint64(sum[7])
	return int(v % uint64(n))
}

// Select chooses one item from the ordered item list. The ORDER is the
// caller's responsibility and must be deterministic (created_at, id): the
// mapping seed->index is stable, so a stable order yields a stable pick.
// Select never mutates the input (调取不改变池).
func Select(poolID, dayUTC string, orderedItemIDs []string) (string, error) {
	if len(orderedItemIDs) == 0 {
		return "", ErrEmptyPool
	}
	return orderedItemIDs[PickIndex(poolID, dayUTC, len(orderedItemIDs))], nil
}
