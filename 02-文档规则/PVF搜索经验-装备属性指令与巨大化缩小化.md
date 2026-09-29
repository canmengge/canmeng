# PVF 数据反查经验（装备属性指令 / 巨大化缩小化）

> 用途：**「某件装备按 Tab（装备属性指令）触发的效果在数据里怎么实现」** 这类问题的标准查法。
> 来源：110 版 `Script.pvf`（Paged110，4,385,215 文件）实测，样例 = `equipment/character/common/jacket/harmor/100080968.equ`（「冤魂的执念上衣」）的 `[缩小化]`。
> 配套：**修改方法（改哪个字段）见 `d:\AA110pvf修改\110资料\knowledge-pack-110\workflows\装备属性指令与巨大化缩小化-反查与修改.md`**；本文件只写"怎么搜、怎么定位"。

---

## 1. 核心口径：装备本体只"声明"，实现全在别处

看到装备有 `[command]` 时不要在这件装备里找效果本体，固定按这条链往下走：

```
.equ（[command] {5=`(USEITEMSKILL)`} 声明 + [create random option] 选词条）
  → 词条清单 .lst（词条 ID → 词条文件）
  → 词条 .etc（[if] [use command] 1 … [then] [appendage] ID ← Tab 在这里接上）
  → list/appendage.lst（ID → APD 路径）
  → .apd（[type] 决定效果种类；[int data]/[float data]/[string data] = 参数与动画）
```

---

## 2. 反查五步（可套用到任何"装备主动效果"）

1. **读 `.equ`**，只找四类：`[command]`（是否 `(USEITEMSKILL)` = 装备属性指令）、`[create random option]`（词条槽，第 3 列是词条 ID）、`[if]/[then]`+`[appendage]`（常驻效果）、`[part set index]`（套装效果在别处）。
2. **词条 ID → 词条文件**：110 版查 `etc/newrandomoption/newrandomoption.lst`（70 版疑为 `etc/randomoption/randomoption.lst`，`需验证`）。
   `.lst` 里的路径**相对该 lst 所在目录**；命名 `_P_` = 部位专属词条、`_R_` = 通用词条。
3. **读词条 `.etc`**：在 `[parameter]` 里找 `[use command] <n>`（= 装备属性指令，实测 1）、`[cooltime]`、`[then] … [equipment duration] <ms> [appendage] <ID>`。
   还要看 `[my state] \`stand\`` / `` \`dash\` `` 这类块（决定"在什么状态下能按出来"）。
4. **ID → APD**：查 `list/appendage.lst`。
   ⚠ **不是所有 `.apd` 都在 `appendage.lst` 里**（本例同目录的 `gianteffect.apd` 就没有 ID，由别处按路径引用）→ 查不到 ID 时别下"文件不存在"的结论。
5. **读 APD 的 `[type]`**：效果种类由 type 决定，PVF 只提供参数。体型类 = `[type] \`giant effect\``，`[int data]` 首位 = 体型百分比（缩小 50 / 巨大化 200，`需验证`）。

---

## 3. 工具与硬坑（必须记住）

CLI：`d:\AA110pvf修改\PVF-Ai-Agent-Workbench-3.0.1\_ai-work\bin\pvf-cli-plus.exe`

```
read   <pvf> <PVF内路径>                  # 渲染成文本
cat    <pvf> <路径1> <路径2>               # 批量渲染
lst    <pvf> list/appendage.lst --out x    # ID → 路径对照表
strget <pvf> <表号> <key>                  # 读字符串表
files  <pvf> <前缀> --out x                 # 按前缀列全部文件（找同类资源最快）
extract <pvf> <目录> <清单.txt> --text-dir D  # 批量导出文本，供本地 grep
```

| # | 坑 | 正确做法 |
| --- | --- | --- |
| 1 | 以为 `search` 能搜文件内容 | **它只匹配"文件路径"**。实测 `create random option`、`buff_basic_explain_100080968` 都是 0 命中（这两个确实在文件里）→ 内容检索必须先 `extract --text-dir` 导出文本，再本地 `Select-String`/grep |
| 2 | 在表 3（equipment）里搜装备文案 | 装备的**词条文案在表 33 = NewRandomOption**（`<33::xxx>`），装备自有说明才是表 3。表号真源 = `list/n_string.lst`（0 基，37 张表） |
| 3 | 想顺手改文案 | 110 安全表白名单只有 **1(Common) / 5(ItemShop) / 8(Npc) / 27(GameServerMsg)**；表 33 不在白名单，改文案有风险 |
| 4 | 按 70 版习惯找路径 | 110 版**清单统一在 `list/`**（`list/appendage.lst` 等），不是 70 版 `equipment/equipment.lst` 那种分散路径 |
| 5 | 用 `search` 找"某 ID 出现在哪" | 同上：**搜 ID 只会命中路径里含该 ID 的文件**，不会命中"文件内容里引用了该 ID"的文件 |

---

## 4. 实测范例（缩小化）

```
100080968.equ
  [command] {5=`(USEITEMSKILL)`}                     ← 声明：接受装备属性指令
  [create random option] 1 -1 27                     ← 槽位1 = 词条 27
        ↓ etc/newrandomoption/newrandomoption.lst
  NewRandomOption_P_27.etc（完整路径：etc/newrandomoption/NewRandomOption/jacket/…）
        [if] [my appendage] 8921 [use command] 1 [cooltime] 60000 [/if]
        [then] [target] `myself` -1 [equipment duration] 25000 [appendage] 8920 [/then]
        [basic explain] {8=`<33::P_27_basic_explain>`}      ← 文案在表 33
        ↓ list/appendage.lst
  8920 → Appendage/equipment/105Lv_Equipment/NewRandomOption/jacket/Tiny.apd
        [type] `giant effect`    [int data] 首位 50     ← 缩到 50%
        [string data] … animation/105Lv_Equipment/NewRandomOption/Tiny/Tiny{Start,End}_00.ani
  对照：同目录 gianteffect.apd  [type] 同为 `giant effect`，[int data] 首位 200（巨大化）
  状态标记：8921 = Tiny_dummy.apd / 8922 = Tiny_dummy2.apd（[type] `dummy`）
```

**边界**：说明文字里的「移动速度 +30% / 回避率 +75% / 无法使用技能和基本攻击」在 PVF 里**没有对应字段**（属客户端对该 type 的内建表现，`需验证`）；同一条词条往往同时挂"状态标记 dummy"+"效果本体"，只看一个会漏。

---

## 5. 一句话速记

> **装备看 `[command]`，效果看词条 `.etc` 的 `[use command]`，落地看 `[appendage] ID`，种类看 APD 的 `[type]`；搜内容不要用 `search`，先导出文本再 grep。**
