// Package tagcsv renders the NFC tag export file consumed by the physical
// NFC writing tool (FEAT-0166 / HUI-1665). The file is pure text: label,
// short code, canonical URL, store name, group name and an (initially empty)
// uid_hint column the operator may fill offline.
//
// 红线:导出面只有 owner 可达(HTTP 层裁决);文件里没有任何服务凭证、租户
// ID、主体/客户信息——列是闭集,构造处不给这些字段。所有以 = + - @ 开头的
// 字段加 ' 前缀(电子表格公式注入防护)。
package tagcsv

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
)

// Header is the fixed column contract for the NFC writing tool.
var Header = []string{"label", "short_code", "url", "store", "group", "uid_hint"}

// bom prefixes the export so Excel opens UTF-8 labels (中文标签) correctly.
var bom = []byte{0xEF, 0xBB, 0xBF}

// Row is one exported tag. Store/Group/UIDHint may be empty.
type Row struct {
	Label     string
	ShortCode string
	URL       string
	Store     string
	Group     string
	UIDHint   string
}

// SanitizeField neutralizes spreadsheet formula injection: a field starting
// with '=' '+' '-' or '@' gets a leading single quote so Excel/Sheets treats
// it as text. Everything else passes through unchanged.
func SanitizeField(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

// Build renders rows as CSV text: BOM + fixed header + sanitized data rows.
func Build(rows []Row) (string, error) {
	var buf bytes.Buffer
	buf.Write(bom)
	w := csv.NewWriter(&buf)
	if err := w.Write(Header); err != nil {
		return "", fmt.Errorf("tagcsv: header: %w", err)
	}
	for _, r := range rows {
		rec := []string{
			SanitizeField(r.Label),
			SanitizeField(r.ShortCode),
			SanitizeField(r.URL),
			SanitizeField(r.Store),
			SanitizeField(r.Group),
			SanitizeField(r.UIDHint),
		}
		if err := w.Write(rec); err != nil {
			return "", fmt.Errorf("tagcsv: row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("tagcsv: flush: %w", err)
	}
	return buf.String(), nil
}

// SanitizeForLog keeps request logs free of raw field content; only the row
// count is ever logged (导出内容绝不入日志).
func Summarize(rows []Row) string {
	return fmt.Sprintf("tagcsv: %d rows, header=%s", len(rows), strings.Join(Header, ","))
}
