# 110 版 PVF 内容地图（DNF / 实测）

> 结论基于本机美服 110 客户端 `D:\115us\DFO_2.31.1.117\`（`Script.pvf` 约 544 MB、**438 万个文件**）。
> 不同区域/版本的客户端可能有差异 —— 结论要复核时，用文末「复核方法」重跑，不要照搬。

## 一、归档里都有什么

用 `scripts/pvf.ps1 -Action extstat` 得到（2026-10-01 实测，前 20 名）：

| 扩展名 | 数量 | 是什么 |
| --- | --- | --- |
| `.ani` | 2,742,646 | 动画定义（帧、图层、坐标） |
| `.equ` | 430,767 | 装备脚本（属性、词条、成长） |
| `.act` | **398,353** | **对象行为脚本**（见第四节） |
| `.als` | 191,202 | 动作序列 / 图层 |
| `.stk` | 154,172 | 可叠加道具 |
| `.obj` | 99,475 | 对象定义 |
| `.til` | 96,812 | 地块 |
| `.atk` | 59,400 | 攻击定义 |
| `.map` | 40,844 | 地图 |
| `.ptl` | 29,313 | 粒子 |
| `.ai` | 24,426 | AI 定义（Lua 之外的配置层） |
| `.key` | 19,556 | 密钥/关键帧类数据 |
| `.mob` | 17,678 | 怪物 |
| `.qst` | 13,389 | 任务 |
| `.etc` | 11,253 | 杂项索引（如 `*_indexhash.etc`） |
| `.apd` | 8,426 | appendage（技能附加状态）**数据** |
| `.xui` | 7,143 | 界面布局 |
| `.skl` | 6,372 | **技能**（见第三节） |
| `.evt` | 6,155 | 事件 |
| `.dgn` | 5,836 | 副本 |
| `.cmt` | 4,024 | 注释 |
| `.npc` | 3,438 | NPC |
| `.cre` | 2,698 | 生物 |
| `.lua` | **2,054** | Lua 脚本（见第二节） |
| `.aic` | 2,036 | AI 配置 |

> **`.nut`（Squirrel）在这个客户端里是 0 个** —— 见第五节。

## 二、Lua 脚本系统（只服务 AI）

规模：2054 个 `.lua`，其中 1974 个在 `.../ai/...` 下；另 80 个是基础库与直挂怪物目录的脚本。

### 挂载机制（关键）

`clientonly/lua/minimumrequire.lua` 把 DNF 资源系统接进 Lua 标准模块加载器：

```lua
UtilBridge = {};
function UtilBridge:readAllTexts(fileName) return ""; end   -- 原生实现会覆盖这个桩
function loadDNFScript(modulename)
    text = UtilBridge:readAllTexts(modulename);
    if text then return assert(loadstring(text)); end
end
package.loaders = {};
table.insert(package.loaders, loadDNFScript);
```

⇒ **`require("<资源路径>")` 直接读归档里的文件**，基础库用的就是绝对路径：
`require("/ClientOnly/Lua/AI/AIStructures.lua")`。

### 一个怪物 AI 脚本要定义 4 个 "pattern 函数"

`clientonly/lua/ai/aibasescriptsnew.lua` 的 `setDefault()` 会挑选（缺了就用 `xxx_default` 兜底）：

| 函数 | 作用 |
| --- | --- |
| `action` | 出招逻辑 |
| `event` | 事件响应 |
| `destinationSelect` | 走位选择 |
| `moveMethod` | 移动方式 |

### 暴露给 Lua 的 API 面（实测统计，约 300 个方法）

| 对象 | 方法数 | 代表接口 |
| --- | --- | --- |
| `Idle` | 37 | `setAction` `setMoveMethod` `isMyState` `isTargetState` `getHpPercent_morethan` `getRandom` `importAI` |
| `Battle` | 21 | `isSkillCooltime` `getHpPercent` `getMpPercent` `isOnDamage` `isOnAttack` `isEnableAttackAction` |
| `Common` | 16 | `setDestinationSelect` `setDestinationSelect_KeepDistance` `getWallCount` `numberOfAttackArea_*` |
| `Patrol` | 18 | `isTargetAttackArea` `isTargetDistance` `isMonsterGroupLeader` `getPartyMemberCount` |
| `DestinationSelect` | 37 | 策略常量：`CHASE` `RUNAWAY` `CHASE_ROUNDABOUT` `MOVE_ZIGZAG` `HIDE_BACK` `BE_SCATTERED` `ESCAPE_WALL` … |
| `MoveMethod` | 3 | `WALK` / `DASH` / `JUMP` |
| `Buff` | 123 | buff 标志位（`IN_OVERCHARGE` `WAVEMARK` …） |
| `State` | 11 | `STATE_DOWN` `STATE_JUMP` `STATE_DIE` `STATE_ATTACK` … |
| `MyInfo` / `EnemyInfo` | 10 / 4 | `currentAction` `nextDestinationSelect` / `targetObjectID` `growType` |
| `AIBridge` | 34 | 原生桥：`getObjectInfo` `lookTarget` `setForceTarget` `useItem` `useUltimateSkill` |
| `UtilBridge` | 5 | `readAllTexts` `intTest` `stringTest` … |
| `Pvp` / `SkillDifficulty` | 3 / 3 | `destroyObjectCheck` / `High·Mid·Low` |

### 桩 vs 真实现（重要）

- `clientonly/lua/ai/aibridge.lua` 里**全是 `return true / -1 / 0` 的桩**；原生注册的同名函数会覆盖它。
- 因此：**Lua 只是"决策层"，动作执行与状态查询在内核**；Lua 能调的能力**仅限内核已导出的那些**。

## 三、技能（`.skl`）数据链

```
技能树 / 技能引用  [<职业>] <技能ID>
        ↓
skill\<职业>skill.lst          技能ID ↔ 文件（实测片段）
        1   `Swordman/Guard.skl`
        8   `Swordman/TripleSlash.skl`
        24  `Swordman/RagingFury.skl`
        ↓
skill\Swordman\TripleSlash.skl  技能本体（实测片段）
        [name]                  {8=`<12::name_18090>`}        ← 字符串表引用（表号 12）
        [explain]               {8=`<12::explain_18095>`}
        [executable states]     `[stand]` `[attack]` `[dash]`
        [purchase cost]         15
        [required level]        15
        [type]                  `[active]`
        [maximum level]         …
```

- 职业技能清单（`config\lists.json` 的 `contextPaths` 全表）：
  `[swordman]→skill/swordmanskill.lst`、`[at swordman]→atswordmanskill.lst`、`[fighter]→fighterskill.lst`、`[gunner]`、`[mage]`、`[priest]`、`[thief]`、`[creator mage]`、`[knight]→knight.lst`、`[demonic swordman]`、`[demonic lancer]` …
- **110 版（paged110）**：ID 必须是 **uint32 数字**，且列表有配套 **indexhash** 文件（`pvf.IndexHashCompanionPath`）。新增 ID **必须同步写 indexhash**，否则客户端可能认不出。
- 工具侧现成入口：**「注册到 lst」**（`services\list_registration.go` 的 `RegisterFileToList`）会**自动算下一个可用 ID** 并**同步写 indexhash**。
- 字符串表：`.skl` 里 `{8=`<12::键名>`}` 的 `8` 是 token 类型（字符串池引用）、`12` 是**表号**；表号↔文件由 `list/n_string.lst` 决定（`internal\pvf\stringtable.go`）。

### 加一个新技能（数据侧步骤）

1. 造 `.skl`：复制现成的改名（`skill\<职业>\<技能名>.skl`），改 `[type]`/`[required level]`/`[maximum level]`/`[purchase cost]` 等段。
2. 注册进 `skill\<职业>skill.lst`：新增 `新ID<TAB>`职业/文件.skl``（用工具的「注册到 lst」）。
3. 字符串表加 `name_*` / `explain_*` 键（工具的「字符串引用」可一键插入并建表项）。
4. 挂到角色技能树（引用该 ID）。
5. （可选）`.act` / `.ani` 做动作与表现；`.apd` 做附加状态。
6. 保存 PVF。

> **风险**：技能 ID 有效区间、技能树槽位、等级上限等**可能有客户端硬编码**。数据格式可验证，"全新 ID 客户端认不认"**只能进游戏实测**。最稳的是**覆盖式**（改现有技能内容）而非新增 ID。

## 四、`.act` —— 对象行为脚本（明文可改）

398,353 个，是 DNF 自家的行为脚本语言（实测原文节选）：

```
[MOTION]
	[BASE ANI]  `../../Animation/NormalAttack/NormalAttack_Body.ani`
	[SUB ANI]   `...NormalAttack_00.ani` 0 -4  ...
	[SOUND EX]  `R_ECLAIR_ATK` 5 0 100 `TRUE` ...
[/MOTION]
[IGNORE HIT STUN]
[TRIGGER]
	[FRAME] 6
	[DO BEHAVIOR] `ME` 1
[/TRIGGER]
[TRIGGER]
	[ON SET ACTION] [WHICH][TARGET][/WHICH]
	[CHECKED NO] [>=] 1
	[DO BEHAVIOR] `CHECKUP OBJECT` 4
[/TRIGGER]
```

⇒ 动作、动画引用、音效、**帧触发**、**条件判断**、行为序列，全部明文可编辑。
`[TRIGGER]` 的写法与可用行为见项目知识库 `知识库\GiteeNut注释\14-ACT脚本说明.md`。

## 五、边界：哪些能改、哪些不能

**能改（数据 / ACT / Lua）**

- `.skl` `.equ` `.stk` `.apd` `.act` `.ai` `.lst` `.str` … 全部是明文可编辑（工具的 `TypeScript` 文本通道）。
- Lua：只能写 **AI 行为**。

**不能改（客户端原生，无脚本接口）**

- 技能/道具/UI/任务的**运行时逻辑** —— Lua 未导出这些接口。
- 证据（扫 `DFO.exe` 212MB 的字符串）：
  - **命中**：`AIBridge` `UtilBridge` `readAllTexts` `getObjectInfo` `isSkillCooltime` `MyInfo` `Patrol` `AIEvent` `DestinationSelect` `nextDestinationSelect` `currentAIEventList`；Lua 运行时痕迹 `package.loaders` `loadstring` `collectgarbage`。
  - **未命中**：`sq_RunScript` `Squirrel` `SquirrelVM` `sq_call` `CNSquirrelAppendage` ⇒ **本客户端没有 Squirrel（.nut）脚本层**。
  - `Appendage` **命中** ⇒ appendage 系统原生仍在，只是**不对脚本开放**。
  - 43 个 DLL 里**都没有** `AIBridge` / `package.loaders` ⇒ Lua 运行时与 AI 桥**都在 `DFO.exe` 内**（静态链接）。

**一个容易误判的坑**：`lua_pcall` / `luaL_newstate` / `luaL_openlibs` 搜不到**不代表没有 Lua** —— 那是函数符号名，发布版 exe 会被 strip。判据要用**字符串常量**。

## 六、复核方法

1. 归档内容：`scripts\pvf.ps1 -Action extstat`；单个文件 `-Action read -Path <路径>`。
2. 客户端能力归属：扫 exe/DLL 的 ASCII 字符串（分块读 + 重叠 48 字节，避免跨块漏匹配），模式用「接口名 / 运行时字符串」，**不要用函数符号名**下结论。
3. 改 `.lua` 的安全性：读文本 → `SetText`（内部 `encodeScript`）→ 再读，比对文本与原始字节（实测 5/5 完全一致，零漂移）。

## 七、本机常用路径

| 用途 | 路径 |
| --- | --- |
| 110 客户端（美服） | `D:\115us\DFO_2.31.1.117\` |
| 主归档 | `D:\115us\DFO_2.31.1.117\Script.pvf`（另有 `D:\115us\Script.pvf`、`全部备份\PVF原版\Script.pvf`、`PVF工具\Script.pvf`） |
| 密钥 | `D:\115us\DFO_2.31.1.117\sk.dat`（工具已内置两套常见密钥） |
| 项目自带 CLI | `d:\110AI\01-源码\cmd\pvf-cli`（读/写/清单/字符串表/结构化变更/batch） |
