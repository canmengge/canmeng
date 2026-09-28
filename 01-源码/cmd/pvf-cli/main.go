// pvf-cli-plus —— 基于 pvfine 库的增强版 PVF 命令行工具
//
// 相对原版（open/read/write/search/list）新增：
//   add      新增文件          del      删除文件
//   info     归档信息(JSON)     files    全量列文件(不截断)
//   dump     批量导出          cat      批量读多个文件
//   strget/strset               字符串表（增量）
//   lst/lstset/lstdel           .lst 清单 id↔路径
//   idxget/idxset               索引哈希表
//   meta                        脚本元数据(JSON)
//   script                      结构化脚本变更(按段 insert/set/delete/number)
//   batch                       【核心】一次打开 → 多操作 → 一次保存
//   saveas                      另存（重建归档）
//
// 设计原则：
//   1) 原 5 个命令的行为保持兼容（write 输出 <源>_modified.pvf）
//   2) 输出默认 <源>_modified.pvf，可用 --out 指定；输出目录缺 sk.dat 时自动补齐
//   3) 只读命令不写盘；写命令一律另行保存，绝不原地覆盖源文件

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"pvfine/internal/pvf"
)

const skFileName = "sk.dat"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	// ---- 基础 ----
	case "open":
		cmdOpen(args)
	case "info":
		cmdInfo(args)
	case "read":
		cmdRead(args)
	case "cat":
		cmdCat(args)
	case "write":
		cmdWrite(args)
	case "add":
		cmdAdd(args)
	case "del", "delete":
		cmdDelete(args)
	case "search":
		cmdSearch(args)
	case "list":
		cmdList(args)
	case "files":
		cmdFiles(args)
	case "dump":
		cmdDump(args)
	case "extract":
		cmdExtract(args)
	// ---- 字符串表 ----
	case "strget":
		cmdStrGet(args)
	case "strset":
		cmdStrSet(args)
	// ---- .lst 清单 ----
	case "lst":
		cmdLst(args)
	case "lstset":
		cmdLstSet(args)
	case "lstdel":
		cmdLstDel(args)
	// ---- 索引哈希 ----
	case "idxget":
		cmdIdxGet(args)
	case "idxset":
		cmdIdxSet(args)
	// ---- 元数据 / 结构化变更 ----
	case "meta":
		cmdMeta(args)
	case "script":
		cmdScript(args)
	case "doc":
		cmdDoc(args)
	// ---- 批量 / 另存 ----
	case "batch":
		cmdBatch(args)
	case "saveas":
		cmdSaveAs(args)
	case "serve":
		cmdServe(args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`pvf-cli-plus  PVF 工具箱（Paged110 / 标准归档通用）

基础
  open   <pvf>                             打开并显示基本信息
  info   <pvf>                             归档信息（JSON）
  read   <pvf> <路径> [--out 文件]          读取单个文件（文本）
  cat    <pvf> <路径...>                    批量读取多个文件（打印）
  write  <pvf> <路径> <本地文件> [--out …]   覆盖已存在文件
  add    <pvf> <路径> <本地文件> [--out …]   新增文件（已存在则报错）
  del    <pvf> <路径...> [--out …]          删除文件
  search <pvf> <关键词> [目录] [--limit N]
  list   <pvf> <目录> [--limit N]
  files  <pvf> [前缀] [--out 文件]           全量列文件（不截断）
  dump   <pvf> <输出目录> [前缀]             批量导出（保持目录结构）
  extract <pvf> <输出目录> <清单> [--raw] [--text-dir D]  按清单批量提取（--raw 原始字节；--text-dir 额外导出渲染文本）

字符串表（.str，索引见 list/n_string.lst）
  strget <pvf> <表索引> <key>
  strset <pvf> <表索引> <key> <值> [--out …]

清单 .lst（id ↔ 路径）
  lst    <pvf> <lst路径> [--out 文件]
  lstset <pvf> <lst路径> <id> <目标路径> [--out …]
  lstdel <pvf> <lst路径> <id...> [--out …]

索引哈希表
  idxget <pvf> <哈希表路径>
  idxset <pvf> <哈希表路径> <id...> [--out …]

元数据与结构化变更
  meta   <pvf> <路径...>                    脚本元数据（JSON：名称/图标等）
  script <pvf> <路径> <变更.json> [--out …]  结构化变更（简化解析器；不支持含 StringLink 的文件）
  doc    <pvf> <路径> <操作.json> [--out …]  文档式精改（支持全部 token 类型，含 StringLink）

批量与保存
  batch  <pvf> <任务.json>                  【推荐】一次打开→多操作→一次保存
  saveas <pvf> <输出路径>                   另存（重建归档）
  serve  <pvf> [--port N] [--idle 秒]        常驻服务：open 一次后缓存，读操作毫秒级（search/list/read/raw/read-batch）

script 的 ops 数组元素（StructuredBatchOperation）：
  { "Kind":"insert|set|delete|number", "Section":"[physical attack]",
    "Value":"30", "Operator":"=", "Operand":"10", "CreateIfMissing":true,
    "AnchorSection":"...", "HasEndTag":true, "TokenIndex":0 }
  Kind=number 时支持 Operator: = + - * / +% -% min max clamp round

batch 任务 JSON：
  {
    "out": "可选输出路径",
    "ops": [
      {"op":"add",     "path":"PVF内路径","file":"本地文件"},
      {"op":"write",   "path":"PVF内路径","file":"本地文件"},
      {"op":"setText", "path":"PVF内路径","text":"直接给文本"},
      {"op":"delete",  "path":"PVF内路径"},
      {"op":"strset",  "table":13,"key":"name_50001248","value":"新名字"},
      {"op":"lstset",  "list":"list/appendage.lst","id":"599990001","path":"appendage/title/599990001.apd"},
      {"op":"lstdel",  "list":"list/appendage.lst","id":"599990001"},
      {"op":"idxset",  "hash":"etc/xxx.etc","id":1234},
      {"op":"script",  "path":"...","ops":[ …StructuredBatchOperation… ]}
    ]
  }
`)
}

// ---------------------------------------------------------------- 通用工具

func openPvf(pvfPath string) *pvf.Archive {
	archive, err := pvf.Open(pvfPath)
	if err != nil {
		log.Fatalf("打开 PVF 失败: %v", err)
	}
	return archive
}

// dataTypeFor 按扩展名推断归档内的数据类型：.str 为 UTF-16 文本，其余为脚本 token 流。
func dataTypeFor(p string) int32 {
	if strings.EqualFold(filepath.Ext(p), ".str") {
		return pvf.TypeUnicode
	}
	return pvf.TypeScript
}

func defaultOut(src string) string {
	if strings.HasSuffix(strings.ToLower(src), ".pvf") {
		return src[:len(src)-4] + "_modified.pvf"
	}
	return src + "_modified.pvf"
}

func sameDir(a, b string) bool {
	pa, err1 := filepath.Abs(a)
	pb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return strings.EqualFold(pa, pb)
}

// ensureSkDat 保证输出目录存在 sk.dat（Paged110 归档必须有它才能打开）。
func ensureSkDat(srcPvf, outPvf string) {
	srcDir := filepath.Dir(srcPvf)
	outDir := filepath.Dir(outPvf)
	if sameDir(srcDir, outDir) {
		return
	}
	dst := filepath.Join(outDir, skFileName)
	if _, err := os.Stat(dst); err == nil {
		return
	}
	b, err := os.ReadFile(filepath.Join(srcDir, skFileName))
	if err != nil {
		fmt.Printf("  提示: 源目录无 %s，若输出为 Paged110 归档需自行放入\n", skFileName)
		return
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		fmt.Printf("  警告: 复制 %s 失败: %v\n", skFileName, err)
		return
	}
	fmt.Printf("  已自动复制 %s 到输出目录\n", skFileName)
}

// readTextTrimBOM 读取文本文件并剥离 UTF-8 BOM。
// Windows 下用记事本/PowerShell 保存的 JSON 常带 BOM，直接交给 encoding/json 会报错。
func readTextTrimBOM(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:], nil
	}
	return b, nil
}

func saveArchive(a *pvf.Archive, srcPvf, out string) {
	if strings.TrimSpace(out) == "" {
		out = defaultOut(srcPvf)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}
	fmt.Printf("正在保存到: %s\n", out)
	if err := a.SaveAs(out); err != nil {
		log.Fatalf("保存失败: %v", err)
	}
	ensureSkDat(srcPvf, out)
	if st, err := os.Stat(out); err == nil {
		fmt.Printf("✓ 保存成功（%.2f MB）\n", float64(st.Size())/1024/1024)
	}
}

// splitOpts 把 ["a","b","--out","x","--limit","5"] 拆成位置参数与选项。
func splitOpts(args []string) ([]string, map[string]string) {
	pos := make([]string, 0, len(args))
	opts := map[string]string{}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			key := strings.TrimPrefix(args[i], "--")
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				opts[key] = args[i+1]
				i++
			} else {
				opts[key] = "true"
			}
			continue
		}
		pos = append(pos, args[i])
	}
	return pos, opts
}

func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

// ---------------------------------------------------------------- 基础命令

func cmdOpen(args []string) {
	if len(args) < 1 {
		log.Fatal("用法: open <pvf文件>")
	}
	a := openPvf(args[0])
	info := a.Info()
	fmt.Println("✓ 打开成功！")
	fmt.Printf("  文件数: %d\n", info.FileCount)
	fmt.Printf("  Paged110 加密: %v\n", info.Paged110)
	fmt.Printf("  数据块数: %d\n", info.GroupCount)
	fmt.Printf("  源文件: %s\n", info.Path)
}

func cmdInfo(args []string) {
	if len(args) < 1 {
		log.Fatal("用法: info <pvf文件>")
	}
	a := openPvf(args[0])
	b, err := json.MarshalIndent(a.Info(), "", "  ")
	if err != nil {
		log.Fatalf("序列化失败: %v", err)
	}
	fmt.Println(string(b))
}

func cmdRead(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: read <pvf文件> <文件路径> [--out 本地文件]")
	}
	a := openPvf(pos[0])
	idx, found := a.Find(pos[1])
	if !found {
		log.Fatalf("未找到文件: %s", pos[1])
	}
	text, err := a.Text(idx)
	if err != nil {
		log.Fatalf("读取失败: %v", err)
	}
	if out := opts["out"]; out != "" {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			log.Fatalf("创建目录失败: %v", err)
		}
		if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
			log.Fatalf("写入失败: %v", err)
		}
		fmt.Printf("✓ 已导出 %s（%d 字节）\n", out, len(text))
		return
	}
	fmt.Print(text)
}

func cmdCat(args []string) {
	pos, _ := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: cat <pvf文件> <文件路径...>")
	}
	a := openPvf(pos[0])
	for _, p := range pos[1:] {
		idx, found := a.Find(p)
		if !found {
			fmt.Printf("===== %s =====\n(未找到)\n\n", p)
			continue
		}
		text, err := a.Text(idx)
		fmt.Printf("===== %s =====\n", p)
		if err != nil {
			fmt.Printf("(读取失败: %v)\n\n", err)
			continue
		}
		fmt.Printf("%s\n\n", text)
	}
}

func cmdWrite(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: write <pvf文件> <文件路径> <本地文件> [--out 输出文件]")
	}
	src, pvfPath, localFile := pos[0], pos[1], pos[2]
	data, err := os.ReadFile(localFile)
	if err != nil {
		log.Fatalf("读取本地文件失败: %v", err)
	}
	a := openPvf(src)
	idx, found := a.Find(pvfPath)
	if !found {
		log.Fatalf("目标不存在（新增请用 add）: %s", pvfPath)
	}
	if err := a.SetText(idx, string(data)); err != nil {
		// .str 等 UTF-16 内容退化为原样字节写入
		if err2 := a.SetRawBytes(idx, data); err2 != nil {
			log.Fatalf("写入失败: %v / %v", err, err2)
		}
	}
	saveArchive(a, src, opts["out"])
}

func cmdAdd(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: add <pvf文件> <文件路径> <本地文件> [--out 输出文件] [--raw]")
	}
	src, pvfPath, localFile := pos[0], pos[1], pos[2]
	data, err := os.ReadFile(localFile)
	if err != nil {
		log.Fatalf("读取本地文件失败: %v", err)
	}
	a := openPvf(src)
	if _, found := a.Find(pvfPath); found {
		log.Fatalf("目标已存在（覆盖请用 write）: %s", pvfPath)
	}
	if opts["raw"] == "true" {
		a.AddFile(pvfPath, data, dataTypeFor(pvfPath))
	} else {
		if _, err := a.AddFileText(pvfPath, string(data), dataTypeFor(pvfPath)); err != nil {
			log.Fatalf("新增失败: %v", err)
		}
	}
	fmt.Printf("✓ 已新增文件: %s（%d 字节）\n", pvfPath, len(data))
	saveArchive(a, src, opts["out"])
}

func cmdDelete(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: del <pvf文件> <文件路径...> [--out 输出文件]")
	}
	src := pos[0]
	a := openPvf(src)
	idxs := make([]int32, 0, len(pos)-1)
	for _, p := range pos[1:] {
		if idx, found := a.Find(p); found {
			idxs = append(idxs, idx)
			fmt.Printf("  待删除: %s\n", p)
		} else {
			fmt.Printf("  跳过(未找到): %s\n", p)
		}
	}
	if len(idxs) == 0 {
		log.Fatal("没有可删除的条目")
	}
	removed, err := a.RemoveFiles(idxs)
	if err != nil {
		log.Fatalf("删除失败: %v", err)
	}
	fmt.Printf("✓ 已删除 %d 个条目\n", len(removed))
	saveArchive(a, src, opts["out"])
}

func cmdSearch(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: search <pvf文件> <关键词> [目录前缀] [--limit N]")
	}
	a := openPvf(pos[0])
	keyword := strings.ToLower(pos[1])
	prefix := ""
	if len(pos) >= 3 {
		prefix = strings.ToLower(pos[2])
	}
	limit := atoiDefault(opts["limit"], 100)
	found := 0
	// 2.0 内核不含 1.0 的 pathList 缓存（属 C2，未合入）⇒ 逐条解析，与旧版 CLI 行为一致（P-2004）
	for i := int32(0); i < a.FileCount(); i++ {
		path := a.Path(i)
		lp := strings.ToLower(path)
		if prefix != "" && !strings.HasPrefix(lp, prefix) {
			continue
		}
		if !strings.Contains(lp, keyword) {
			continue
		}
		fmt.Println(path)
		found++
		if limit > 0 && found >= limit {
			fmt.Printf("... (已达上限 %d)\n", limit)
			break
		}
	}
	fmt.Printf("\n共找到 %d 个匹配文件\n", found)
}

func cmdList(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 1 {
		log.Fatal("用法: list <pvf文件> <目录> [--limit N]")
	}
	a := openPvf(pos[0])
	dir := ""
	if len(pos) >= 2 {
		dir = pos[1]
	}
	limit := atoiDefault(opts["limit"], 200)
	found := 0
	// 2.0 内核不含 1.0 的 pathList 缓存（属 C2，未合入）⇒ 逐条解析，与旧版 CLI 行为一致（P-2004）
	for i := int32(0); i < a.FileCount(); i++ {
		path := a.Path(i)
		if dir != "" {
			if !strings.HasPrefix(path, dir) {
				continue
			}
			rest := path[len(dir):]
			if strings.Contains(rest, "/") {
				continue
			}
		}
		fmt.Println(path)
		found++
		if limit > 0 && found >= limit {
			fmt.Printf("... (已达上限 %d)\n", limit)
			break
		}
	}
	fmt.Printf("\n共找到 %d 个文件\n", found)
}

func cmdFiles(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 1 {
		log.Fatal("用法: files <pvf文件> [前缀] [--out 文件]")
	}
	a := openPvf(pos[0])
	prefix := ""
	if len(pos) >= 2 {
		prefix = pos[1]
	}
	var sb strings.Builder
	n := 0
	// 2.0 内核不含 1.0 的 pathList 缓存（属 C2，未合入）⇒ 逐条解析，与旧版 CLI 行为一致（P-2004）
	for i := int32(0); i < a.FileCount(); i++ {
		path := a.Path(i)
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			continue
		}
		sb.WriteString(path)
		sb.WriteString("\n")
		n++
	}
	if out := opts["out"]; out != "" {
		if err := os.WriteFile(out, []byte(sb.String()), 0o644); err != nil {
			log.Fatalf("写入失败: %v", err)
		}
		fmt.Printf("✓ 已导出 %d 条路径到 %s\n", n, out)
		return
	}
	fmt.Print(sb.String())
	fmt.Printf("\n共 %d 个文件\n", n)
}

func cmdDump(args []string) {
	pos, _ := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: dump <pvf文件> <输出目录> [前缀]")
	}
	a := openPvf(pos[0])
	outDir := pos[1]
	prefix := ""
	if len(pos) >= 3 {
		prefix = pos[2]
	}
	n := 0
	failed := 0
	err := a.ForEachRawFile(context.Background(), func(idx int32, path string, file pvf.File, raw []byte) bool {
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			return true
		}
		text, err := a.Text(idx)
		if err != nil {
			failed++
			fmt.Printf("  跳过(读取失败) %s: %v\n", path, err)
			return true
		}
		out := filepath.Join(outDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			failed++
			return true
		}
		if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
			failed++
			return true
		}
		n++
		if n%500 == 0 {
			fmt.Printf("  已导出 %d ...\n", n)
		}
		return true
	})
	if err != nil {
		log.Fatalf("遍历失败: %v", err)
	}
	fmt.Printf("✓ 导出完成：成功 %d / 失败 %d\n输出目录: %s\n", n, failed, outDir)
}

// cmdExtract 按清单批量提取：一次打开 PVF，把清单里列出的文件全部导出（保持目录结构）。
// 用于生成"改动前快照"（回滚点）——比逐个 read 快得多（一次解密 vs N 次）。
//   --raw            输出目录导出原始字节（RawBytes），供字节级精确回滚
//   --text-dir DIR   额外把渲染文本（Text）导出到 DIR，供人读/diff
// 二者可同时用：同一次 open 产出「raw + 文本」两份，省掉第二次全量解密（≈6.5 秒）。
func cmdExtract(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: extract <pvf文件> <输出目录> <路径清单文件> [--raw] [--text-dir <目录>]")
	}
	a := openPvf(pos[0])
	outDir := pos[1]
	rawMode := opts["raw"] == "true"
	textDir := opts["text-dir"]
	rawList, err := readTextTrimBOM(pos[2])
	if err != nil {
		log.Fatalf("读取清单失败: %v", err)
	}
	writeFile := func(dir, p string, data []byte) error {
		out := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	}
	lines := strings.Split(strings.ReplaceAll(string(rawList), "\r\n", "\n"), "\n")
	okN, missN, failN := 0, 0, 0
	for _, ln := range lines {
		p := strings.TrimSpace(ln)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		idx, found := a.Find(p)
		if !found {
			fmt.Printf("  缺失(不导出): %s\n", p)
			missN++
			continue
		}
		failed := false
		if rawMode {
			raw, rerr := a.RawBytes(idx)
			if rerr != nil {
				fmt.Printf("  读取失败: %s (%v)\n", p, rerr)
				failed = true
			} else if err := writeFile(outDir, p, raw); err != nil {
				failed = true
			}
		} else {
			text, terr := a.Text(idx)
			if terr != nil {
				fmt.Printf("  读取失败: %s (%v)\n", p, terr)
				failed = true
			} else if err := writeFile(outDir, p, []byte(text)); err != nil {
				failed = true
			}
		}
		if !failed && textDir != "" {
			text, terr := a.Text(idx)
			if terr != nil {
				fmt.Printf("  文本导出失败: %s (%v)\n", p, terr)
				failed = true
			} else if err := writeFile(textDir, p, []byte(text)); err != nil {
				failed = true
			}
		}
		if failed {
			failN++
			continue
		}
		okN++
	}
	mode := map[bool]string{true: "raw 原始字节", false: "渲染文本"}[rawMode]
	if textDir != "" {
		mode += " + 文本(" + textDir + ")"
	}
	fmt.Printf("✓ 提取完成：成功 %d / 缺失 %d / 失败 %d（模式: %s）\n输出目录: %s\n",
		okN, missN, failN, mode, outDir)
}

// ---------------------------------------------------------------- 字符串表

func cmdStrGet(args []string) {
	pos, _ := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: strget <pvf文件> <表索引> <key>")
	}
	idx := atoiDefault(pos[1], -1)
	if idx < 0 {
		log.Fatalf("表索引非法: %s", pos[1])
	}
	a := openPvf(pos[0])
	val, ok := a.LookupStringTable(idx, pos[2])
	if !ok {
		fmt.Printf("(未找到 %s)\n", pos[2])
		os.Exit(2)
	}
	fmt.Println(val)
}

func cmdStrSet(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 4 {
		log.Fatal("用法: strset <pvf文件> <表索引> <key> <值> [--out 输出文件]")
	}
	idx := atoiDefault(pos[1], -1)
	if idx < 0 {
		log.Fatalf("表索引非法: %s", pos[1])
	}
	src := pos[0]
	a := openPvf(src)
	tablePath, err := a.SetStringTableEntry(idx, pos[2], pos[3])
	if err != nil {
		log.Fatalf("写入字符串表失败: %v", err)
	}
	fmt.Printf("✓ 已写入 %s: %s = %s\n", tablePath, pos[2], pos[3])
	saveArchive(a, src, opts["out"])
}

// ---------------------------------------------------------------- .lst 清单

func cmdLst(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: lst <pvf文件> <lst路径> [--out 文件]")
	}
	a := openPvf(pos[0])
	idx, ok := a.FindList(pos[1])
	if !ok {
		log.Fatalf("未找到清单: %s", pos[1])
	}
	pairs, err := a.ScriptListPairs(idx)
	if err != nil {
		log.Fatalf("解析清单失败: %v", err)
	}
	var sb strings.Builder
	for _, p := range pairs {
		sb.WriteString(p.ID)
		sb.WriteString("\t")
		sb.WriteString(p.Path)
		sb.WriteString("\n")
	}
	if out := opts["out"]; out != "" {
		if err := os.WriteFile(out, []byte(sb.String()), 0o644); err != nil {
			log.Fatalf("写入失败: %v", err)
		}
		fmt.Printf("✓ 已导出 %d 条到 %s\n", len(pairs), out)
		return
	}
	fmt.Print(sb.String())
	fmt.Printf("\n共 %d 条\n", len(pairs))
}

func cmdLstSet(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 4 {
		log.Fatal("用法: lstset <pvf文件> <lst路径> <id> <目标路径> [--out 输出文件]")
	}
	src := pos[0]
	a := openPvf(src)
	idx, ok := a.FindList(pos[1])
	if !ok {
		log.Fatalf("未找到清单: %s", pos[1])
	}
	if err := a.SetListPair(idx, pos[2], pos[3]); err != nil {
		log.Fatalf("写入清单失败: %v", err)
	}
	fmt.Printf("✓ %s: %s -> %s\n", pos[1], pos[2], pos[3])
	saveArchive(a, src, opts["out"])
}

func cmdLstDel(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: lstdel <pvf文件> <lst路径> <id...> [--out 输出文件]")
	}
	src := pos[0]
	a := openPvf(src)
	idx, ok := a.FindList(pos[1])
	if !ok {
		log.Fatalf("未找到清单: %s", pos[1])
	}
	n, err := a.RemoveListIDs(idx, pos[2:])
	if err != nil {
		log.Fatalf("删除条目失败: %v", err)
	}
	fmt.Printf("✓ %s: 删除 %d 条\n", pos[1], n)
	saveArchive(a, src, opts["out"])
}

// ---------------------------------------------------------------- 索引哈希

func cmdIdxGet(args []string) {
	pos, _ := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: idxget <pvf文件> <哈希表路径>")
	}
	a := openPvf(pos[0])
	pairs, err := a.IndexHashPairs(pos[1])
	if err != nil {
		log.Fatalf("读取失败: %v", err)
	}
	b, _ := json.MarshalIndent(pairs, "", "  ")
	fmt.Println(string(b))
}

func cmdIdxSet(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: idxset <pvf文件> <哈希表路径> <id...> [--out 输出文件]")
	}
	src := pos[0]
	ids := make([]uint32, 0, len(pos)-2)
	for _, s := range pos[2:] {
		v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
		if err != nil {
			log.Fatalf("id 非法: %s", s)
		}
		ids = append(ids, uint32(v))
	}
	a := openPvf(src)
	if err := a.SetIndexHashEntriesForIDs(pos[1], ids); err != nil {
		log.Fatalf("写入失败: %v", err)
	}
	fmt.Printf("✓ %s: 已写入 %d 个 id\n", pos[1], len(ids))
	saveArchive(a, src, opts["out"])
}

// ---------------------------------------------------------------- 元数据 / 结构化

func cmdMeta(args []string) {
	pos, _ := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: meta <pvf文件> <文件路径...>")
	}
	a := openPvf(pos[0])
	out := make([]map[string]interface{}, 0, len(pos)-1)
	for _, p := range pos[1:] {
		idx, found := a.Find(p)
		rec := map[string]interface{}{"path": p, "found": found}
		if found {
			md, err := a.ScriptMetadata(idx)
			if err != nil {
				rec["error"] = err.Error()
			} else {
				rec["name"] = md.Name
				rec["hasName"] = md.HasName
			}
		}
		out = append(out, rec)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

// scriptOpsFile 是 script 命令的 ops 文件格式。
type scriptOpsFile struct {
	Ops []pvf.StructuredBatchOperation `json:"ops"`
}

func cmdScript(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: script <pvf文件> <文件路径> <变更.json> [--out 输出文件]")
	}
	src := pos[0]
	a := openPvf(src)
	idx, found := a.Find(pos[1])
	if !found {
		log.Fatalf("未找到文件: %s", pos[1])
	}
	raw, err := a.RawBytes(idx)
	if err != nil {
		log.Fatalf("读取原始数据失败: %v", err)
	}
	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)

	var spec scriptOpsFile
	data, err := readTextTrimBOM(pos[2])
	if err != nil {
		log.Fatalf("读取变更文件失败: %v", err)
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		// 兼容直接给数组的写法
		var arr []pvf.StructuredBatchOperation
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			log.Fatalf("解析变更文件失败: %v", err)
		}
		spec.Ops = arr
	}
	if len(spec.Ops) == 0 {
		log.Fatal("变更列表为空")
	}

	res, err := a.TransformStructuredBatch(rawCopy, spec.Ops)
	if err != nil {
		log.Fatalf("结构化变更失败: %v", err)
	}
	for _, w := range res.Warnings() {
		fmt.Printf("  警告: %s\n", w)
	}
	fmt.Printf("  匹配 %d 处，%s\n", res.MatchCount(), map[bool]string{true: "内容已变更", false: "内容未变"}[res.Changed()])
	if !res.Changed() {
		log.Fatal("没有任何变更，未保存")
	}
	if err := a.SetRawBytes(idx, res.Raw()); err != nil {
		log.Fatalf("写回失败: %v", err)
	}
	saveArchive(a, src, opts["out"])
}

// ---------------------------------------------------------------- 批量引擎

type batchOp struct {
	Op       string          `json:"op"`
	Path     string          `json:"path"`
	File     string          `json:"file"`
	Text     *string         `json:"text"`
	Table    int             `json:"table"`
	Key      string          `json:"key"`
	Value    string          `json:"value"`
	List     string          `json:"list"`
	ID       string          `json:"id"`
	Hash     string          `json:"hash"`
	IDs      []uint32        `json:"ids"`
	Ops      json.RawMessage `json:"ops"`
	Raw      bool            `json:"raw"`
	Required bool            `json:"required"`
}

type batchTask struct {
	Out string    `json:"out"`
	Ops []batchOp `json:"ops"`
}

func cmdBatch(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: batch <pvf文件> <任务.json>")
	}
	src := pos[0]
	data, err := readTextTrimBOM(pos[1])
	if err != nil {
		log.Fatalf("读取任务文件失败: %v", err)
	}
	var task batchTask
	if err := json.Unmarshal(data, &task); err != nil {
		log.Fatalf("解析任务文件失败: %v", err)
	}
	if len(task.Ops) == 0 {
		log.Fatal("任务里没有任何操作")
	}
	out := task.Out
	if out == "" {
		out = opts["out"]
	}

	a := openPvf(src)
	applied := 0
	skipped := 0

	for i, op := range task.Ops {
		tag := fmt.Sprintf("[%d/%d] %s", i+1, len(task.Ops), op.Op)
		switch strings.ToLower(strings.TrimSpace(op.Op)) {
		case "add":
			if op.Path == "" || op.File == "" {
				log.Fatalf("%s 缺少 path 或 file", tag)
			}
			if _, found := a.Find(op.Path); found {
				fmt.Printf("%s 跳过(已存在，未覆盖): %s\n", tag, op.Path)
				skipped++
				continue
			}
			b, err := os.ReadFile(op.File)
			if err != nil {
				log.Fatalf("%s 读取本地文件失败: %v", tag, err)
			}
			if op.Raw {
				a.AddFile(op.Path, b, dataTypeFor(op.Path))
			} else if _, err := a.AddFileText(op.Path, string(b), dataTypeFor(op.Path)); err != nil {
				log.Fatalf("%s 新增失败: %v", tag, err)
			}
			fmt.Printf("%s 新增 %s（%d 字节）\n", tag, op.Path, len(b))
			applied++

		case "write", "settext":
			if op.Path == "" {
				log.Fatalf("%s 缺少 path", tag)
			}
			idx, found := a.Find(op.Path)
			var content []byte
			if op.Text != nil {
				content = []byte(*op.Text)
			} else if op.File != "" {
				b, err := os.ReadFile(op.File)
				if err != nil {
					log.Fatalf("%s 读取本地文件失败: %v", tag, err)
				}
				content = b
			} else {
				log.Fatalf("%s 需要 file 或 text", tag)
			}
			if !found {
				// upsert：目标不存在则新增（旧 ID 冲突时直接用新内容替换）
				if op.Raw {
					a.AddFile(op.Path, content, dataTypeFor(op.Path))
				} else if _, err := a.AddFileText(op.Path, string(content), dataTypeFor(op.Path)); err != nil {
					log.Fatalf("%s 新增失败: %v", tag, err)
				}
				fmt.Printf("%s 新增(upsert) %s（%d 字节）\n", tag, op.Path, len(content))
				applied++
				continue
			}
			if op.Raw {
				if err := a.SetRawBytes(idx, content); err != nil {
					log.Fatalf("%s 写入失败: %v", tag, err)
				}
			} else if err := a.SetText(idx, string(content)); err != nil {
				if err2 := a.SetRawBytes(idx, content); err2 != nil {
					log.Fatalf("%s 写入失败: %v / %v", tag, err, err2)
				}
			}
			fmt.Printf("%s 覆盖 %s（%d 字节%s）\n", tag, op.Path, len(content),
				map[bool]string{true: ", 原始字节", false: ""}[op.Raw])
			applied++

		case "delete", "del":
			if op.Path == "" {
				log.Fatalf("%s 缺少 path", tag)
			}
			idx, found := a.Find(op.Path)
			if !found {
				fmt.Printf("%s 跳过(未找到): %s\n", tag, op.Path)
				skipped++
				continue
			}
			if _, err := a.RemoveFiles([]int32{idx}); err != nil {
				log.Fatalf("%s 删除失败: %v", tag, err)
			}
			fmt.Printf("%s 删除 %s\n", tag, op.Path)
			applied++

		case "strset":
			if op.Key == "" {
				log.Fatalf("%s 缺少 key", tag)
			}
			tablePath, err := a.SetStringTableEntry(op.Table, op.Key, op.Value)
			if err != nil {
				log.Fatalf("%s 字符串表写入失败: %v", tag, err)
			}
			fmt.Printf("%s 字符串表 %s: %s = %s\n", tag, tablePath, op.Key, op.Value)
			applied++

		case "lstset":
			idx, ok := a.FindList(op.List)
			if !ok {
				log.Fatalf("%s 未找到清单: %s", tag, op.List)
			}
			if err := a.SetListPair(idx, op.ID, op.Path); err != nil {
				log.Fatalf("%s 清单写入失败: %v", tag, err)
			}
			fmt.Printf("%s 清单 %s: %s -> %s\n", tag, op.List, op.ID, op.Path)
			applied++

		case "lstdel":
			idx, ok := a.FindList(op.List)
			if !ok {
				log.Fatalf("%s 未找到清单: %s", tag, op.List)
			}
			n, err := a.RemoveListIDs(idx, []string{op.ID})
			if err != nil {
				log.Fatalf("%s 清单删除失败: %v", tag, err)
			}
			fmt.Printf("%s 清单 %s: 删除 %d 条（id=%s）\n", tag, op.List, n, op.ID)
			applied++

		case "idxset":
			ids := op.IDs
			if len(ids) == 0 && op.ID != "" {
				v, err := strconv.ParseUint(op.ID, 10, 32)
				if err != nil {
					log.Fatalf("%s id 非法: %s", tag, op.ID)
				}
				ids = []uint32{uint32(v)}
			}
			if err := a.SetIndexHashEntriesForIDs(op.Hash, ids); err != nil {
				log.Fatalf("%s 索引哈希写入失败: %v", tag, err)
			}
			fmt.Printf("%s 索引哈希 %s: %d 个 id\n", tag, op.Hash, len(ids))
			applied++

		case "script":
			idx, found := a.Find(op.Path)
			if !found {
				log.Fatalf("%s 未找到文件: %s", tag, op.Path)
			}
			var ops []pvf.StructuredBatchOperation
			if len(op.Ops) > 0 {
				if err := json.Unmarshal(op.Ops, &ops); err != nil {
					log.Fatalf("%s 解析 ops 失败: %v", tag, err)
				}
			}
			if len(ops) == 0 {
				log.Fatalf("%s ops 为空", tag)
			}
			rawNow, err := a.RawBytes(idx)
			if err != nil {
				log.Fatalf("%s 读取原始数据失败: %v", tag, err)
			}
			rawCopy := make([]byte, len(rawNow))
			copy(rawCopy, rawNow)
			res, err := a.TransformStructuredBatch(rawCopy, ops)
			if err != nil {
				log.Fatalf("%s 结构化变更失败: %v", tag, err)
			}
			for _, w := range res.Warnings() {
				fmt.Printf("%s   警告: %s\n", tag, w)
			}
			if err := a.SetRawBytes(idx, res.Raw()); err != nil {
				log.Fatalf("%s 写回失败: %v", tag, err)
			}
			fmt.Printf("%s 结构化变更 %s：匹配 %d 处\n", tag, op.Path, res.MatchCount())
			applied++

		default:
			log.Fatalf("%s 不支持的操作类型: %s", tag, op.Op)
		}
	}

	if applied == 0 {
		fmt.Println("没有任何改动，未保存")
		return
	}
	fmt.Printf("共应用 %d 项操作（跳过 %d 项）\n", applied, skipped)
	saveArchive(a, src, out)
}

// ---------------------------------------------------------------- 文档式精改（支持全部 token 类型）

type docOp struct {
	Kind       string   `json:"kind"`       // set | setValues | delete | appendSection | appendChild
	Path       []string `json:"path"`       // 段路径，如 ["[piece set ability]"]
	Occurrence int      `json:"occurrence"` // 同名段序号（从 0 开始）
	ValueIndex int      `json:"valueIndex"` // 段内第几个值（从 0 开始）
	Value      string   `json:"value"`      // 新值（文本形式）
	ValueType  string   `json:"valueType"`  // auto | integer | float | string | quoted
	Values     []string `json:"values"`     // setValues / append* 用
	Name       string   `json:"name"`       // 新建段名
	Create     bool     `json:"create"`     // set 时段不存在时是否创建
	EndTag     bool     `json:"endTag"`     // 新段是否带闭合标签
}

type docOpsFile struct {
	Ops []docOp `json:"ops"`
}

// makeScriptValue 按类型构造脚本值；auto 会依次尝试整数、小数，最后落到 quoted。
func makeScriptValue(text, valueType string) (pvf.ScriptValue, error) {
	switch strings.ToLower(strings.TrimSpace(valueType)) {
	case "integer", "int":
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 32)
		if err != nil {
			return pvf.ScriptValue{}, fmt.Errorf("整数解析失败: %q", text)
		}
		return pvf.NewScriptValue(pvf.ScriptTokenInteger, float64(n), pvf.ScriptPoolUTF8)
	case "float":
		f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return pvf.ScriptValue{}, fmt.Errorf("小数解析失败: %q", text)
		}
		return pvf.NewScriptValue(pvf.ScriptTokenFloat, f, pvf.ScriptPoolUTF8)
	case "string":
		return pvf.NewScriptValue(pvf.ScriptTokenString, text, pvf.ScriptPoolUTF8)
	case "quoted":
		return pvf.NewScriptValue(pvf.ScriptTokenQuoted, text, pvf.ScriptPoolUTF8)
	default:
		if n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 32); err == nil {
			return pvf.NewScriptValue(pvf.ScriptTokenInteger, float64(n), pvf.ScriptPoolUTF8)
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
			return pvf.NewScriptValue(pvf.ScriptTokenFloat, f, pvf.ScriptPoolUTF8)
		}
		return pvf.NewScriptValue(pvf.ScriptTokenQuoted, text, pvf.ScriptPoolUTF8)
	}
}

func makeScriptValues(texts []string, valueType string) ([]pvf.ScriptValue, error) {
	out := make([]pvf.ScriptValue, 0, len(texts))
	for _, t := range texts {
		v, err := makeScriptValue(t, valueType)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func cmdDoc(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 3 {
		log.Fatal("用法: doc <pvf文件> <文件路径> <操作.json> [--out 输出文件]")
	}
	src := pos[0]
	a := openPvf(src)
	idx, found := a.Find(pos[1])
	if !found {
		log.Fatalf("未找到文件: %s", pos[1])
	}
	raw, err := a.RawBytes(idx)
	if err != nil {
		log.Fatalf("读取原始数据失败: %v", err)
	}
	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)

	doc, err := a.ParseScriptDocument(rawCopy)
	if err != nil {
		log.Fatalf("解析脚本失败: %v", err)
	}

	data, err := readTextTrimBOM(pos[2])
	if err != nil {
		log.Fatalf("读取操作文件失败: %v", err)
	}
	var spec docOpsFile
	if err := json.Unmarshal(data, &spec); err != nil {
		var arr []docOp
		if err2 := json.Unmarshal(data, &arr); err2 != nil {
			log.Fatalf("解析操作文件失败: %v", err)
		}
		spec.Ops = arr
	}
	if len(spec.Ops) == 0 {
		log.Fatal("操作列表为空")
	}

	applied := 0
	for i, op := range spec.Ops {
		tag := fmt.Sprintf("[%d/%d] %s", i+1, len(spec.Ops), op.Kind)
		switch strings.ToLower(strings.TrimSpace(op.Kind)) {
		case "set":
			v, err := makeScriptValue(op.Value, op.ValueType)
			if err != nil {
				log.Fatalf("%s %v", tag, err)
			}
			ok, err := doc.Set(op.Path, op.Occurrence, op.ValueIndex, v, op.Create, op.EndTag)
			if err != nil {
				log.Fatalf("%s 设置失败: %v", tag, err)
			}
			fmt.Printf("%s %v 第%d段 值%d = %s（已改变=%v）\n", tag, op.Path, op.Occurrence, op.ValueIndex, op.Value, ok)
			applied++

		case "setvalues":
			sec, ok := doc.Section(op.Path, op.Occurrence)
			if !ok {
				log.Fatalf("%s 未找到段: %v 第%d段", tag, op.Path, op.Occurrence)
			}
			vals, err := makeScriptValues(op.Values, op.ValueType)
			if err != nil {
				log.Fatalf("%s %v", tag, err)
			}
			if err := sec.SetValues(vals); err != nil {
				log.Fatalf("%s 设置失败: %v", tag, err)
			}
			fmt.Printf("%s %v 整段值已替换（%d 个）\n", tag, op.Path, len(vals))
			applied++

		case "delete":
			ok, err := doc.Delete(op.Path, op.Occurrence)
			if err != nil {
				log.Fatalf("%s 删除失败: %v", tag, err)
			}
			fmt.Printf("%s 删除 %v 第%d段（已改变=%v）\n", tag, op.Path, op.Occurrence, ok)
			applied++

		case "appendsection":
			vals, err := makeScriptValues(op.Values, op.ValueType)
			if err != nil {
				log.Fatalf("%s %v", tag, err)
			}
			if _, err := doc.AppendSection(op.Name, vals, op.EndTag); err != nil {
				log.Fatalf("%s 追加失败: %v", tag, err)
			}
			fmt.Printf("%s 根级追加段 %s\n", tag, op.Name)
			applied++

		case "appendchild":
			sec, ok := doc.Section(op.Path, op.Occurrence)
			if !ok {
				log.Fatalf("%s 未找到父段: %v 第%d段", tag, op.Path, op.Occurrence)
			}
			vals, err := makeScriptValues(op.Values, op.ValueType)
			if err != nil {
				log.Fatalf("%s %v", tag, err)
			}
			if _, err := sec.AppendSection(op.Name, vals, op.EndTag); err != nil {
				log.Fatalf("%s 追加子段失败: %v", tag, err)
			}
			fmt.Printf("%s 在 %v 下追加子段 %s\n", tag, op.Path, op.Name)
			applied++

		default:
			log.Fatalf("%s 不支持的操作类型: %s", tag, op.Kind)
		}
	}

	for _, w := range doc.Warnings() {
		fmt.Printf("  解析警告: %+v\n", w)
	}

	out, err := a.EncodeScriptDocument(doc)
	if err != nil {
		log.Fatalf("编码失败: %v", err)
	}
	if err := a.SetRawBytes(idx, out); err != nil {
		log.Fatalf("写回失败: %v", err)
	}
	fmt.Printf("共应用 %d 项操作，编码后 %d 字节\n", applied, len(out))
	saveArchive(a, src, opts["out"])
}

func cmdSaveAs(args []string) {
	pos, _ := splitOpts(args)
	if len(pos) < 2 {
		log.Fatal("用法: saveas <pvf文件> <输出路径>")
	}
	a := openPvf(pos[0])
	saveArchive(a, pos[0], pos[1])
}

// ---------------------------------------------------------------- 常驻服务（读操作缓存）

type serveReq struct {
	Path    string   `json:"path"`
	Paths   []string `json:"paths"`
	Keyword string   `json:"keyword"`
	Prefix  string   `json:"prefix"`
	Dir     string   `json:"dir"`
	Limit   int      `json:"limit"`
}

// cmdServe 启动常驻 HTTP 服务：open 一次 PVF 后把 Archive 缓存在内存，
// 后续只读请求（search/list/files/read/raw/read-batch）直接查内存，省掉每次
// ~6.5 秒的全量读取 + Paged110 解密。空闲超时自动退出（--idle 秒，默认 600）。
func cmdServe(args []string) {
	pos, opts := splitOpts(args)
	if len(pos) < 1 {
		log.Fatal("用法: serve <pvf文件> [--port 端口] [--idle 空闲超时秒]")
	}
	pvfPath := pos[0]
	port := opts["port"]
	idle := time.Duration(atoiDefault(opts["idle"], 600)) * time.Second

	a := openPvf(pvfPath)
	var pvfMtime int64
	if st, err := os.Stat(pvfPath); err == nil {
		pvfMtime = st.ModTime().UnixMilli()
	}

	// 串行化请求：Archive 的惰性 chunk 缓存非并发安全
	var mu sync.Mutex
	lastActive := time.Now()
	handle := func(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			lastActive = time.Now()
			fn(w, r)
		}
	}
	writeJSON := func(w http.ResponseWriter, v interface{}) {
		b, _ := json.Marshal(v)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
	// 路径编码：含非法 UTF-8 的路径用 base64 包裹（b64: 前缀），避免 json.Marshal 清洗成 U+FFFD
	const maxServeResults = 100000 // search/list 硬上限，防极端大结果占数百 MB
	encodePaths := func(paths []string) []string {
		out := make([]string, len(paths))
		for i, p := range paths {
			if utf8.ValidString(p) {
				out[i] = p
			} else {
				out[i] = "b64:" + base64.StdEncoding.EncodeToString([]byte(p))
			}
		}
		return out
	}
	findAndRead := func(w http.ResponseWriter, p string, raw bool) {
		idx, found := a.Find(p)
		if !found {
			http.Error(w, "未找到文件: "+p, 404)
			return
		}
		var out []byte
		if raw {
			b, err := a.RawBytes(idx)
			if err != nil {
				http.Error(w, "读取失败: "+err.Error(), 500)
				return
			}
			out = b
		} else {
			text, err := a.Text(idx)
			if err != nil {
				http.Error(w, "读取失败: "+err.Error(), 500)
				return
			}
			out = []byte(text)
		}
		// 原始字节直传，避免 json.Marshal 把无效 UTF-8 替换成 U+FFFD（.str 表含 mojibake 修复后的非法字节）
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(out)
	}
	decode := func(r *http.Request, q *serveReq) error {
		return json.NewDecoder(r.Body).Decode(q)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", handle(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	mux.HandleFunc("/info", handle(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"info":  a.Info(),
			"path":  pvfPath,
			"mtime": pvfMtime,
		})
	}))
	mux.HandleFunc("/shutdown", handle(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bye"))
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(0) }()
	}))
	mux.HandleFunc("/search", handle(func(w http.ResponseWriter, r *http.Request) {
		var q serveReq
		_ = decode(r, &q)
		kw := strings.ToLower(q.Keyword)
		prefix := strings.ToLower(q.Prefix)
		limit := q.Limit
		if limit <= 0 || limit > maxServeResults {
			limit = maxServeResults
		}
		paths := make([]string, 0, limit)
		total := 0
		truncated := false
		// 2.0 内核不含 1.0 的 pathList 缓存（属 C2，未合入）⇒ 逐条解析（P-2004）
		for i := int32(0); i < a.FileCount(); i++ {
			p := a.Path(i)
			lp := strings.ToLower(p)
			if prefix != "" && !strings.HasPrefix(lp, prefix) {
				continue
			}
			if !strings.Contains(lp, kw) {
				continue
			}
			total++
			if len(paths) < limit {
				paths = append(paths, p)
			} else {
				truncated = true
			}
		}
		writeJSON(w, map[string]interface{}{"paths": encodePaths(paths), "count": len(paths), "total": total, "truncated": truncated})
	}))
	mux.HandleFunc("/list", handle(func(w http.ResponseWriter, r *http.Request) {
		var q serveReq
		_ = decode(r, &q)
		limit := q.Limit
		if limit <= 0 || limit > maxServeResults {
			limit = maxServeResults
		}
		paths := make([]string, 0, limit)
		total := 0
		truncated := false
		// 2.0 内核不含 1.0 的 pathList 缓存（属 C2，未合入）⇒ 逐条解析（P-2004）
		for i := int32(0); i < a.FileCount(); i++ {
			p := a.Path(i)
			if q.Dir != "" {
				if !strings.HasPrefix(p, q.Dir) {
					continue
				}
				if strings.Contains(p[len(q.Dir):], "/") {
					continue
				}
			}
			total++
			if len(paths) < limit {
				paths = append(paths, p)
			} else {
				truncated = true
			}
		}
		writeJSON(w, map[string]interface{}{"paths": encodePaths(paths), "count": len(paths), "total": total, "truncated": truncated})
	}))
	mux.HandleFunc("/files", handle(func(w http.ResponseWriter, r *http.Request) {
		var q serveReq
		_ = decode(r, &q)
		paths := make([]string, 0)
		// 2.0 内核不含 1.0 的 pathList 缓存（属 C2，未合入）⇒ 逐条解析（P-2004）
		for i := int32(0); i < a.FileCount(); i++ {
			p := a.Path(i)
			if q.Prefix != "" && !strings.HasPrefix(p, q.Prefix) {
				continue
			}
			paths = append(paths, p)
		}
		writeJSON(w, map[string]interface{}{"paths": encodePaths(paths), "count": len(paths)})
	}))
	mux.HandleFunc("/read", handle(func(w http.ResponseWriter, r *http.Request) {
		var q serveReq
		_ = decode(r, &q)
		findAndRead(w, q.Path, false)
	}))
	mux.HandleFunc("/raw", handle(func(w http.ResponseWriter, r *http.Request) {
		var q serveReq
		_ = decode(r, &q)
		findAndRead(w, q.Path, true)
	}))
	mux.HandleFunc("/read-batch", handle(func(w http.ResponseWriter, r *http.Request) {
		var q serveReq
		_ = decode(r, &q)
		items := make([]map[string]interface{}, 0, len(q.Paths))
		for _, p := range q.Paths {
			idx, found := a.Find(p)
			if !found {
				items = append(items, map[string]interface{}{"path": p, "found": false})
				continue
			}
			text, err := a.Text(idx)
			if err != nil {
				items = append(items, map[string]interface{}{"path": p, "found": false, "error": err.Error()})
				continue
			}
			// base64 承载文本字节，避免 json.Marshal 的 UTF-8 清洗
			items = append(items, map[string]interface{}{"path": p, "found": true, "base64": base64.StdEncoding.EncodeToString([]byte(text))})
		}
		writeJSON(w, map[string]interface{}{"items": items})
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		log.Fatalf("监听失败: %v", err)
	}
	// 空闲超时自动退出，避免常驻进程泄漏
	go func() {
		for {
			time.Sleep(1 * time.Second)
			mu.Lock()
			idleFor := time.Since(lastActive)
			mu.Unlock()
			if idleFor > idle {
				os.Exit(0)
			}
		}
	}()
	fmt.Printf("SERVE_READY %s\n", ln.Addr().String())
	_ = http.Serve(ln, mux)
}
