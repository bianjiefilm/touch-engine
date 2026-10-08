// epoch_audit_test.go: HUI-2981 后续加固——失效完备性机器审计。
//
// 缓存的正确性取决于失效完备性：readcache 缓存的 payload（ResolvedRows，
// 见 store_readcache.go）消费哪些表，这些表的每一条写路径就必须触发
// epoch bump——否则管理员改完数据、公共页吃陈旧缓存。此前这条纪律只靠
// 「写代码时记得 bump」+ E2E 抽样；本测试把它降为机器可查的不变量：
//
//	缓存表的每一条 SQL 写（AST 扫描 store 包全部非测试源文件的字符串
//	字面量，词边界精确匹配表名）→ 其所属函数（或一跳内被调函数）必须
//	调用 bumpEpoch；确属无需 bump 的写点必须在 auditExemptions 登记
//	理由，非空豁免即显式决策。
//
// 边界与约定：
//   - 审计范围 = server/internal/store（仓内唯一 SQL 写层）；若出现第二
//     个写层，必须把它纳入本审计。
//   - SQL 中表名保持字面量（不得经变量拼接表名），否则审计不可见。
//   - bumpEpoch 改名时 bump 集合变空 → 所有写点全部违规 → 本测试必红，
//     不会静默通过。
package store

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// auditCachedTables: 缓存 payload（ResolvedRows）消费的全部表。真源=
// store_readcache.go；行集字段增删时同步此表（注释指向本测试）。
var auditCachedTables = []string{"campaign_links", "campaign", "stores", "tenants"}

// auditMinWrites: 现存缓存表写点下限。低于此数说明审计正则/表名失配
// （例如表改名后 cachedTables 未同步，审计静默空转）——先修真源再放行。
const auditMinWrites = 8

// auditExemptions: 确认无需 bump 的缓存表写点（"函数名@表" → 理由）。
// 保持为空是默认态；新增豁免=一次显式的设计决策，须同时更新
// docs/superpowers/specs/2026-10-08-hui-2981-cache-t1-design.md 的失效边界一节。
var auditExemptions = map[string]string{}

// auditWriteRe: 词边界匹配缓存表的写语句（INSERT [OR x] INTO / UPDATE /
// DELETE FROM）。\b 保证 campaign 不会命中 campaign_rules。
var auditWriteRe = regexp.MustCompile(
	`(?i)(?:INSERT\s+(?:OR\s+[A-Z]+\s+)?INTO|UPDATE|DELETE\s+FROM)\s+` +
		`(` + strings.Join(auditCachedTables, "|") + `)\b`)

func TestEpochBumpCoversCachedTableWrites(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse store package: %v", err)
	}

	type fnInfo struct {
		writes map[string]bool // 缓存表名 → 该函数体内出现其写语句
		calls  []string        // 直接调用的同包函数名
	}
	fns := map[string]*fnInfo{}
	add := func(name string) *fnInfo {
		if fns[name] == nil {
			fns[name] = &fnInfo{writes: map[string]bool{}}
		}
		return fns[name]
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				info := add(fd.Name.Name)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch v := n.(type) {
					case *ast.BasicLit:
						if m := auditWriteRe.FindStringSubmatch(v.Value); m != nil {
							info.writes[strings.ToLower(m[1])] = true
						}
					case *ast.CallExpr:
						var callee string
						switch fn := v.Fun.(type) {
						case *ast.Ident:
							callee = fn.Name
						case *ast.SelectorExpr:
							if id, ok := fn.X.(*ast.Ident); ok && id.Name == "s" {
								callee = fn.Sel.Name // s.xxx —— 同 Store 方法
							}
						}
						if callee != "" && callee != fd.Name.Name {
							info.calls = append(info.calls, callee)
						}
					}
					return true
				})
			}
		}
	}

	// bump 集合：直接调用 bumpEpoch 的函数，传递闭合一跳以上（helper 链）。
	bumping := map[string]bool{}
	for name, info := range fns {
		for _, c := range info.calls {
			if c == "bumpEpoch" {
				bumping[name] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for name, info := range fns {
			if bumping[name] {
				continue
			}
			for _, c := range info.calls {
				if bumping[c] {
					bumping[name] = true
					changed = true
					break
				}
			}
		}
	}

	var violations []string
	totalWrites := 0
	for name, info := range fns {
		for table := range info.writes {
			totalWrites++
			if bumping[name] {
				continue
			}
			key := name + "@" + table
			if reason, ok := auditExemptions[key]; ok {
				if strings.TrimSpace(reason) == "" {
					violations = append(violations,
						fmt.Sprintf("%s: 豁免已登记但理由为空（豁免必须写明设计依据）", key))
				}
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s: 写缓存表 %q 但函数（含一跳被调链）不触发 bumpEpoch；若确属无需失效，请在 auditExemptions 登记理由", key, table))
		}
	}
	if totalWrites < auditMinWrites {
		t.Fatalf("审计仅命中 %d 个缓存表写点（下限 %d）：表名/正则与真源失配，拒绝在空转状态下放行", totalWrites, auditMinWrites)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("失效完备性违规 %d 处：\n%s", len(violations), strings.Join(violations, "\n"))
	}
}
