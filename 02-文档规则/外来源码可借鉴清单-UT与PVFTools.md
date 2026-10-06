# 外来源码可借鉴清单（pvfUtility / PVFTools）

> 位置：`d:\110AI\02-文档规则\外来源码可借鉴清单-UT与PVFTools.md`
> 用途：**PVF工坊（编辑器）可拓展 / 可优化项的全量清单**。用户圈定编号后按项目规矩开工（动 Go 走「构建→核验→告知」三步，改完提交打标签）。
> **用户决策版（先看那份）**：`编辑器可加功能一览.md` —— 同一编号体系，按「作用 / 优先级 / 成本 / 风险」组织；本文件只管施工细节。
> 最后更新：2026-10-06
> 相关文档：`源码落点速查.md`（落点+踩坑）、`编辑器内核红线.md`（F1/F2/F3 冻结层）、`01-项目开发规则.md` §0/§C

---

## 0. 三个来源与"可用性"判定（前提，先看这段）

| 来源 | 路径 | 技术栈 | 可搬性 |
| --- | --- | --- | --- |
| **pvfUtility-master** | `D:\AA110pvf修改\pvfUtility2026-v3.0\pvfUtility-master\`（1894 `.cs`） | C# WPF，**反编译还原产物** | **不复制代码文本**（栈不同 + 无 LICENSE + 反编译产物）⇒ **只借能力 / 交互 / 字段规则**，用 Go + Vue 重写 |
| **PVFTools** | `D:\AA110pvf修改\pvftools-main\`（72 `.go` / 26 `.vue`） | **Wails v2 + Vue3 + Go** | 与我们同栈，可借**结构**；**无 LICENSE** ⇒ 实现重写。注意它**没有归档读写层**（数据源只有「UT HTTP」或「散文件目录」`pvf_source/*`），搬来必须换成我们的 `internal/pvf` |
| **godof**（PVFTools 的依赖库） | `C:\Users\Administrator\go\pkg\mod\github.com\dof-dev\godof@v1.1.0\` | Go，**Apache-2.0** | **可合法复用**（保留 LICENSE 与声明）。但它的 `pvf_reader/` 只认**老格式**（明文头 + `DecryptCrc`），**无 AES/RSA/页密钥**；有价值的是 `parser/`、`npk/img/` |

> 结论口径：**能力照搬、代码重写**。唯一可整段复用的是 `godof`（Apache-2.0）。

---

## A. 编辑器体验类

来源：`pvfUtility-master\PvfCode\Controls\`、`PvfCode\ViewModels\DocumentFolder\`

| # | 能力 | 来源文件 | 我们现状 | 备注 |
| --- | --- | --- | --- | --- |
| A1 | **脚本代码补全 + 参数提示（Insight/Overload）+ 用户自定义补全数据** | `DocumentFolder\CodeCompletion\*`（13 个：`CompletionList.cs` 9.9K、`WindowCompletion.cs` 11.8K、`InsightWindow.cs`、`OverloadInsightWindow.cs`、`NutCodeCompletion.cs`、`ScriptCodeCompletion.cs`、`WindowAddCodeCompletionData*.cs`） | **已补齐**（2026-10-06，构建 4.10.2）：后端 `FormViewService.CompletionCatalog` 下发**全量段名** + 段内字段/枚举取值；前端 `pvfCompletionSource` 扩到全量段名并按位置给取值/字段提示 | 仍缺：Nut 脚本**参数提示（Insight/Overload，可走 TS 语言服务签名提示）**与用户自定义补全数据 |
| A2 | **代码折叠（按段 / 括号）+ 折叠边栏 + 折叠引导线** | `DocumentFolder\Foldings\*`（10 个：`FoldingStrategyBase.cs` 13.5K、`FoldingMargin.cs` 13K、`TabFoldingStrategy.cs`、`BraceFoldingStrategy.cs`、`FoldingGuideLines.cs`） | **已有**（2026-10-06） | `codeFolding()` + `foldGutter()` + 自定义 `pvfFoldService`（按 `[段]` 折叠）+ `foldKeymap`；只装普通文件通道，**别圈** |
| A3 | **悬浮提示体系**：段注释 / 物品编号 / 文件路径 / 折叠 | `DocumentFolder\EditorHoverTooltip\*`（9 个，`EditorHoverTooltipManager.cs` **24.4K**） | **已撤销**（2026-10-06）：当天做过一版正文 token 悬浮提示，与**自带注解胶囊提示重复**（同一份注解、两个入口），用户要求只保留自带 ⇒ 已回退 | 若将来要做，只做**折叠提示**（折起占位 hover）这一类，不要再重复胶囊已有的三类 |
| A4 | **段内注释行渲染**（注释画进行内） | `DocumentFolder\VisualLineElementGenerators\ScriptCommentLineGenerator.cs` 8.9K | **已撤销**（2026-10-06）：当天做过一版行尾释义（`buildLineDigest` + `LineDigestWidget`），与自带提示功能重叠，用户要求去掉 ⇒ 已回退 | 若将来重做，必须先确认与自带注解提示**不重复** |
| A5 | **同名词高亮 / 长行截断 / 搜索命中行背景标色** | `OffsetColorizers\MarkSameWord.cs`、`VisualLineElementGenerators\TruncateLongLines.cs`、`BackgroundRenderers\SearchResultBackgroundRenderer.cs` | **取消（不做）** | 同名高亮已有（`highlightSelectionMatches()`）；命中标色已有（官方 `cm-searchMatch` + 大文件自绘 2026-10-06）；长行**按用户要求折行、不截断**（2026-09-27 明确） |
| A6 | **滚动条里标记 + 增强滚动条**（VS 式打点） | `VerticalScrollBarHighlighted\*`、`BackgroundRenderers\EnhancedScrollBar.cs` 6.9K | **部分已有**（2026-10-06 v1）：自绘 fixed 覆盖层（`CodeEditor.vue` 的 `overview` 计算 + `syncOverviewBox` 定位），红=查重问题行、绿=未保存改动区，点击跳行 | **已做 v2**（2026-10-06）：内容搜索命中也打点（黄）；通道是新增的 `frontend\src\searchMarks.ts`（弹窗发布 → 编辑器按文件索引读），关面板/清结果即清点 |
| A7 | **行内错误标记服务** | `TextMarker\TextMarkerService.cs` 7K | **已接两类**（2026-10-06）：① 清单查重问题行（`EditorPane.vue` 的 `problemsFor()`）② **脚本编译诊断**（`ScriptWorkbench.vue` 的 `scriptProblems`）→ 同一个 `problems` prop ⇒ 行底红 + 行号旁红点 + 原生 `title` 说明原因；编辑正文即清空 | Doctor 归档体检查身**不带行号**，要画上行标记得先给它行号（Go 侧改动） |
| A8 | **跳转行 / 编辑器内搜索面板** | `Controls\TextEditorFolder\WindowGotoLine.cs`、`SearchPanel.cs` 6.7K | **已有**（2026-10-06） | `Ctrl+G` 跳行 + `Ctrl+F` 段内搜索面板，两编辑器共用 `../searchPanel.ts` 中文文案；**别圈** |
| A9 | **跨文档搜索管理器** | `DocumentFolder\SearchViewModels\DocumentsSearchManager.cs` 13.1K、`SearchViewModel.cs` 14.6K | **部分已有**（2026-10-06）：`AdvancedSearchModal.vue` 的 `openItem` 改为 `revealInFile`（打开即定位命中）+ 新增 `stepHit` / F3 / Shift+F3 跨文档前后步进（不关面板、环绕） | **已收口**（2026-10-06，构建 4.10.3）：面板新增「内容搜索」开关（走 `AdvancedSearch("string",…)`）；后端 `AdvancedSearchDetail` 加 `Line`（`services\advanced_search.go`），在**分页出口**用现成的 `lineOffsetsFor`（`services\large_text.go:106`）+ `lineOfOffset`（`services\file_search.go:153`）对命中 `Value` 定位换算 —— 关键：分页拆成 `paginateAdvancedViewLocked`（持锁取页）+ 锁外 `fillAdvancedLines`（取文本会写缓存，锁内会死锁）；前端按行号 `revealFileLine` 精确跳转 | 跨文档替换、搜索历史未做 |
| A10 | 文件路径可点击跳转 | `LinkFolder\FilePathLinkVisualLine.cs` 10.5K | **已有**（Ctrl+单击） | 不重复做 |
| A11 | **语法高亮定义（36 份 `.xshd`）** | `SourceLibraries\HL\*.xshd` | **已对照补齐 3 处**（2026-10-06）：`//` 注释按行吃掉 + `tags.lineComment` 轻量斜体；KOR `<…>` 同行闭合串按 string 着色；`{}` 按 `tags.bracket` 着色（三者都走显式 `tokenTable` 映射）。36 份里仅 `Script / Lst / StringTable / Kor` 与 PVF 相关（其余 31 份是通用语言、Lua 无对照） | `.str` 的 `索引 > 值`、`::` 细分**不做**（低价值且易误伤）；`#` 不上色等有意取舍不动 |

## B. 解析与格式规则（最贴合 P0）

| # | 能力 | 来源文件 | 我们现状 | 备注 |
| --- | --- | --- | --- | --- |
| B1 | **30 个特殊段专属格式规则** | `SourceLibraries\PvfCode.Services\PvfCode\Services\PvfParsingNew\CustomSectionFormat\*`（`independent_drop`、`independent_drop_list`、`Worlddrop`、`map_monster`、`etc_shp`、`Stk_Blueprint`、`Stk_upgradable_legacy`、`recipelistmakeequip`、`ShopItemList`、`qst_reward__selection_int_data`、`RegenerationPrice`、`EquipmentLotteryList`、`PremiumTerm`、`RandomCategory`、`VariableGroup`、`UpgradeEffect`、`SpecialPassiveObject(Item)`、`LevelInfo`、`DungeonName`、`DungeonPartyBalance`、`compoundavatar`、`BookTitle`、`AvatarSelectAbility`、`CountPrefixedGrid`…） | `config\formats.json` 规则 | **正对 P0-①`[dungeon condition]` 补规则** |
| B2 | **表格行格式化**（固定列 / 两列 / 五列 / 物品字典） | `PvfParsingNew\TableFormatters\*`（5 个） | FormView 表格视图 | 提升可读性 |
| B3 | **PraseInfo「每行 token 数」规则** | `PvfParsingNew\PraserInfoProviderConfiger.cs` 37K、`ScriptFileParserNew.cs` 41K | **数据已落地未消费**（待办 P4-21） | 直接接 |

## C. 预览与图片

| # | 能力 | 来源文件 | 我们现状 | 备注 |
| --- | --- | --- | --- | --- |
| C1 | **`.ani` 二进制编译 / 反编译** | `PvfCode.Services\PvfCode\Services\BinaryAniCompiler.cs` **43.6K** | 只有只读 `AniPreview.vue` | **正对 P4-22 内核缺口**，要碰红线，单独一轮 |
| C2 | **装备预览数据构建（词条 / 套装）** | `PreviewPvfFileFolder\FilePreviewData_Equ.cs` 11.6K、`PvfFilePreviewHelper.cs` 29K、`EquPartsetFile.cs`、`PieceSetAbility.cs` | 有 `EquipmentPreview.vue` | 可补齐套装 |
| C3 | **时装「选择能力」预览** | `PreviewPvfFileFolder\avatar_select_ability*.cs` | 无 | |
| C4 | **技能树 / 套装技能解析** | `DocumentFolder\PvfSkillTreeParser.cs`、`PvfEquipmentSetSkillDataParser.cs`、`PvfBoosterPreviewParser.cs` | 无 | |
| C5 | **NpcShop 预览** | `PreviewPvfFileFolder\NpcShop\*` | 无 | 配合 D2 |
| C6 | **NPK 读取 / 图标表** | `PvfCode.Services\PvfCode\Services\Npk\NpkImageArchiveReader.cs` 12.9K、`ImagePack2Service.cs` 23.9K；`godof\npk\img\`（v1/v2/v4/v5 + 1555/4444/8888 格式） | 有缩略图 / 预览 | 对照补格式 |
| C7 | ANI / NPK 内联预览窗口 | `DocumentFolder\AniNpkLineElement\*` | 有 `AniPreview.vue` | 对照 |

## D. 数据编辑新功能

来源：`pvfUtility-master\PvfCode\Views\`、`pvfUtility-master\PvfCode\ViewModels\`、`pvftools-main\backend\`、`pvftools-main\frontend\src\views\`

| # | 能力 | 来源文件 | 我们现状 |
| --- | --- | --- | --- |
| D1 | **掉率管理 / 世界掉落**（怪物掉率·深渊掉率·翻牌掉率·世界掉落） | UT：`Views\Tools\DropRateManagementWindow.cs` 9.9K、`Views\Tools\WorlddropTool*.cs`、`ViewModels\DropRateManagement\DropRateDocument.cs` 9K；PVFTools：`views\WorldDropView.vue`/`MonsterDrop.vue`/`HellDrop.vue`/`ClearRewardDrop.vue` + `internal\world_drop\mgr.go` | 无 |
| D2 | **NPC 商店编辑器 / 新建商店条目** | UT：`Views\NpcShopEditor\*`（`WinNpcShopEditor.cs` 8K、`NpcShopEditorViewModel.cs` 19.8K、`NpcShopItem.cs` 7.3K）、`Views\PvfTreeFolder\CreateShopItem.cs`、`Views\Tools\ShopManager\WinShopManager.cs` | 无 |
| D3 | **独立掉落编辑** | UT：`Views\independent_drop\*` + `ViewModels\independent_drop\*`（`Independent_drop_ViewModel.cs` 23.4K）+ B1 的 `independent_drop*` 格式 | **已有**（P0 同一功能）⇒ 拿来对照 / 补齐 |
| D4 | **LST 工具集**（排序 / 合并 / 批量） | UT：`Views\LstTools\WinLstTools.cs` + `WinLstToolsViewModel.cs` 16.9K | 有查重，缺工具集 |
| D5 | **批量重命名 / 树内剪贴板** | UT：`ViewModels\TreeFolder\ViewReNmaeNodesViewMode.cs` 15K、`TreeFileClipboardManager.cs` | 待确认 |
| D6 | **归档 / 文件差异对比** | UT：`Views\Diff\WinNewDiffEditor.cs`、`ViewModels\PvfDiffTool\PvfDiffToolViewModel.cs` 16.9K、`PvfCode\PvfTreeFileDiff.cs` | 有版本面板，缺 diff |
| D7 | **宏工具（录制 / 回放批量操作）** | UT：`Views\Macro\*`（7 个，`WindowMacroToolViewModel.cs` 12.7K） | 无（有批处理） |
| D8 | **ANI 设计器（可视化编辑）** | UT：`Views\AniDesigner\*` + `Controls\NpkImageControls\AniImageControl.cs` | 无 |
| D9 | **礼盒生成器（随机 / 选择）** | PVFTools：`views\BoxView.vue` + `internal\box\{random_box,selection_box}.go`；UT：`ServiceStackable.cs` 14.8K | 无 |
| D10 | **装备属性模板生成** | PVFTools：`frontend\src\resources\equ_attr.json` 25K（名称 → PVF 字段模板 + 参数顺序）+ `views\EquAttrView.vue` | 无 |
| D11 | **技能数据批量修改** | PVFTools：`views\SkillView.vue`、`SkillIncrementList.vue` + `internal\data_loader\skill.go`；UT：`PvfCode\PvfSkillClassifier.cs`、`PvfSkillDataParameters.cs` | 无 |
| D12 | **异次元气息编辑** | PVFTools：`views\BreathView.vue` + `internal\breath\editor.go` + `backend\proto\breath.go` | 无 |
| D13 | **强化 / 增幅数据修改** | PVFTools：`views\UpgradeView.vue` + `internal\upgrade\upgrade.go` | 无 |
| D14 | **任务生成器** | PVFTools：`views\QuestView.vue` | 无 |
| D15 | **批量处理视图** | PVFTools：`views\BatchHandleView.vue`；UT：`ServiceBatchOperation.cs` 10K | 有 `BatchProcessModal.vue` 32K ⇒ 对照 |
| D16 | 通用搜索 | PVFTools：`components\AnySearch.vue` | **已有**高级搜索 ⇒ 不做 |
| D17 | **AI 助手体系**（工具目录 / 知识包服务 / OpenAI 兼容网关 / 会话与补全） | UT：`PvfCode\AiAssistant\*`（9 个，`PvfAssistantToolCatalog.cs` 21.7K、`KnowledgePackService.cs` 10.3K、`OpenAiCompatibleChatGateway.cs`、`AiAssistantAgent.cs`）；`SourceLibraries\Whetstone.ChatGPT\*`（70 个） | 有 `AiPanel.vue` 13K ⇒ 可大幅升级 |
| D18 | **Markdown 渲染** | UT：`Controls\MarkdownDocumentViewer.cs` 19K、`MarkdownEditorPreview.cs` | 无（知识库有 md） |

## E. 归档与发布

| # | 能力 | 来源文件 | 我们现状 | 备注 |
| --- | --- | --- | --- | --- |
| E1 | **一键发布到客户端 / 服务端** | `PvfCode.Services\PvfCode\Services\PvfRelease\ServicePvfReleaseToClient.cs` **35.8K**、`ToServer.cs` | 有外置 `替换PVF.cmd` | ⚠ **L0 红线**：客户端 PVF 禁止自动替换，必须手动确认 |
| E2 | 导出 / 导入文件服务（含 7z） | `ServiceExtractFiles.cs`、`ServiceImportFiles.cs` | 有导出 / 导入 | 对照 |
| E3 | 汉化包转换 | `ServiceConvertChinaPlusPvf.cs` 11.1K | 无 | 小众 |
| E4 | 内存清理 / 游戏进程检测 | `Services\TimeServices\*` | 无 | 对应 P2 内存 |
| E5 | 虚拟化大列表集合 | `SourceLibraries\Swordfish.NET.CollectionsV3\` | 有 `LargeScrollView.vue` | 可参考 |
| E6 | 资源合并工具（含 `audit` 命令） | `tools\PvfResourceMerger\ResourceMerger.cs` | 无 | NPK / 资源合并 |

## F. 明确不做

`Views\WindowPvfFileHeaderEdit.cs`（改 PVF 头 → 撞内核红线）、`Store\*`、`UserViews\WindowLogin*`、`CloudBackup\*`、`Admin\WinAdvertisingTool.cs`、`GMTool\*`（`SourceLibraries\GMTool.SqlModel\` 356 个 `.cs` 的 SQL 化 GM 工具）、`ServiceCloud*` / `CloudOptions*`、`Converts\*`（72 个 WPF 值转换器）、`ICSharpCode.AvalonEdit\`（编辑器库，我们用 CodeMirror）、`docs\*`（反混淆/迁移文档，我们已不碰 115）。

---

## G. 建议的开工批次

| 批次 | 内容 | 理由 |
| --- | --- | --- |
| **第 1 批（快赢）** | A1 代码补全 / A2 折叠 / A8 跳转行 / A5 同名词高亮 | 纯前端，HMR 秒验，不碰 Go，零回归风险 |
| **第 2 批（对口 P0）** | B1 特殊段格式规则（先 `[dungeon condition]`）、B2 表格行格式化、B3 PraseInfo 接入 | 只改 `config\formats.json` 与投影；按 P0-①「抽真实文件校准」流程做 |
| **第 3 批（新功能）** | D1 掉率管理 / 世界掉落 → D2 NPC 商店编辑器 → D9 礼盒生成器 → D10 装备属性模板 | 独立 service + 独立视图，不碰冻结层 |
| **第 4 批（硬骨头，单独一轮）** | C1 `.ani` 编译 / 内联图片、C2/C4 套装与技能树预览 | 要碰内核冻结层 ⇒ 走 `编辑器内核红线.md`（F1 不改实现 / F2 五项 bench 不变差 / F3 不在 Open 与 buildIndex 间插同步全量工作） |

## H. 落地纪律（照 `01-项目开发规则.md`）

1. 新功能一律**新建独立 Vue 视图 + 独立 Go service**，只在工具栏 / 可视化编辑区下拉加一个入口，**不碰 `internal\pvf\` 冻结层**。
2. 数据读写复用既有**结构化改写引擎**；写盘仍只由用户点「保存 PVF」触发。
3. 改 Go ⇒ 执行 §0「强制收尾三步：构建 → 核验 → 告知」；改前端 ⇒ HMR，不构建。
4. 每轮落地并验证后 `commit` + 打 `v<版本>` 标签（纯文档用 `v<版号>-doc<n>`）。
