/**
 * 导出相关新增调用的适配层（与 `importApi.ts` / `objectViewApi.ts` 同源做法）。
 *
 * `frontend/bindings/` 由 `wails3 generate bindings` 生成、本机无法重建（模块缓存里
 * `build_assets/windows/msix` 为空，`//go:embed` 直接编译失败），因此新增的 Go 方法
 * 统一走 `Call.ByName`，不碰 `bindings/`。
 */
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/**
 * ExportFilesTo 把所选条目导出到 `dir` 下新建的「<时间戳><kind>」文件夹，返回实际写入目录。
 * kind 为空时按「文件导出」命名；「导出改动」传「改动文件导出」。
 * 目录由前端自绘的目录选择器给出（`FolderPickerModal` 的 dirs 模式）。
 * fqn = pvfine/services.EditorService.ExportFilesTo
 */
export function ExportFilesTo(
  dir: string,
  scopes: string[],
  kind: string
): $CancellablePromise<string> {
  return $Call.ByName("pvfine/services.EditorService.ExportFilesTo", dir, scopes, kind);
}
