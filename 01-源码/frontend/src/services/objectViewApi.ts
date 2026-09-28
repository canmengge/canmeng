/**
 * 对象视图服务的调用适配层（**临时**）。
 *
 * ## 为什么不在 frontend/bindings/ 里
 *
 * `AGENTS.md` 规定 `frontend/bindings/` 由 `wails3 generate bindings` 生成、禁手工修改。
 * 但本机**无法构建 wails3 CLI**：模块缓存里 `internal/commands/build_assets/windows/msix`
 * 与 `.../nsis` 是空目录，`//go:embed build_assets/windows/msix/*` 直接编译失败
 * （见 pvf-dev-project/work/fnv-probe/ 与 P-0004 补丁说明的"已知局限"）。
 * 因此这里放一个独立适配层，**不碰** `bindings/`。
 *
 * ## 方法 ID 不是猜的
 *
 * 算法取自 wails v3.0.0-beta.12：
 *   - `pkg/application/bindings.go:245`  fqn := "<PkgPath>.<TypeName>.<MethodName>"
 *   - `pkg/application/bindings.go:251`  methodID := hash.Fnv(fqn)
 *   - `internal/hash/fnv.go:5`           fnv.New32a()（即 FNV-1a-32）
 *
 * 并用**仓库内已有 bindings 的真实 ID 回归验证 7/7 全部吻合**
 * （BookmarkService ×4、ArchiveService ×2、EditorService ×1，覆盖大小写混合的方法名）。
 * 复算探针：`pvf-dev-project/work/fnv-probe/`（纯标准库，`go run .`）。
 *
 * ## 何时删除本文件
 *
 * 官方生成器可用后（例如补全 build_assets 后 `go run github.com/wailsapp/wails/v3/cmd/wails3
 * generate bindings -clean=true -ts -i`），改用 `bindings/pvfine/services` 的生成结果，
 * 并删除本文件。届时此处的接口与 Go 侧 JSON tag 应逐字段一致。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.ObjectTypeInfo`。 */
export interface ObjectTypeInfo {
  id: string;
  label: string;
  listPaths: string[];
  extensions?: string[];
  /** 显示文本所在字符串表号；缺省表示不做表查询。 */
  stringTable?: number | null;
  notes?: string;
}

/** 对应 Go `services.ObjectTypeListResult`。 */
export interface ObjectTypeListResult {
  rulePath: string;
  objectTypeCount: number;
  types: ObjectTypeInfo[];
}

/** 对应 Go `services.ObjectViewFile`。role: script / list / indexHash。 */
export interface ObjectViewFile {
  role: string;
  path: string;
  /** -1 表示归档内不存在。 */
  fileIndex: number;
  exists: boolean;
}

/** 对应 Go `services.ObjectViewText`。origin: script（脚本占位符）/ pattern（键名模式）。 */
export interface ObjectViewText {
  tableIndex: number;
  key: string;
  value: string;
  source?: string;
  found: boolean;
  fallback: boolean;
  origin: string;
}

/** 对应 Go `services.ObjectViewRegistration`。 */
export interface ObjectViewRegistration {
  listPath: string;
  listFileIndex: number;
  category: string;
  id: string;
  entryPath: string;
}

/** 对应 Go `services.ObjectView`。 */
export interface ObjectView {
  objectType: string;
  objectLabel: string;
  objectId: string;
  name?: string;
  files: ObjectViewFile[];
  texts: ObjectViewText[];
  registrations: ObjectViewRegistration[];
  warnings: string[];
}

/**
 * 返回数据文件里定义的全部对象类型。
 * fqn = pvfine/services.ObjectViewService.ListObjectTypes
 */
export function ListObjectTypes(): $CancellablePromise<ObjectTypeListResult> {
  return $Call.ByID(410976597);
}

/**
 * 把一个对象 ID 聚合为可审阅的视图。
 * fqn = pvfine/services.ObjectViewService.ResolveObject
 */
export function ResolveObject(
  objectType: string,
  objectID: string
): $CancellablePromise<ObjectView> {
  return $Call.ByID(2474875886, objectType, objectID);
}

/**
 * 重新读取对象类型数据文件。
 * fqn = pvfine/services.ObjectViewService.ReloadRules
 */
export function ReloadRules(): $CancellablePromise<ObjectTypeListResult> {
  return $Call.ByID(2367133665);
}
