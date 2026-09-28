/**
 * 「输出日志」面板的后端调用适配层（与 `objectViewApi.ts` 同源做法）。
 *
 * `frontend/bindings/` 由 wails3 生成、禁手工修改，而本机无法构建 wails3 CLI
 * （模块缓存缺 build_assets，见 objectViewApi.ts 的说明），因此新增方法用
 * `$Call.ByID` 直接调用。
 *
 * 方法 ID = FNV-1a-32("<PkgPath>.<TypeName>.<MethodName>")
 * （wails v3 `pkg/application/bindings.go` + `internal/hash/fnv.go`），
 * 已用仓库内既有 bindings 六项 ID 回归校验通过。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `logging.Entry`。 */
export interface LogEntry {
  time: string;
  level: string;
  module: string;
  message: string;
  fields?: string;
  text?: string;
}

/** History 返回启动以来的最近日志（最多 512 条，旧的在前）。 */
export function History(): $CancellablePromise<LogEntry[] | null> {
  return $Call.ByID(2321590517);
}

/** Clear 清空服务端历史缓冲（不影响日志文件）。 */
export function Clear(): $CancellablePromise<void> {
  return $Call.ByID(1309695284);
}
