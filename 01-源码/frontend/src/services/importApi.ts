/**
 * 导入相关新增调用的适配层（与 `objectViewApi.ts` 同源做法）。
 *
 * ## 为什么不在 frontend/bindings/ 里
 *
 * `frontend/bindings/` 由 `wails3 generate bindings` 生成、禁止手工修改；本机
 * **无法构建 wails3 CLI**（模块缓存里 `build_assets/windows/msix` 与 `nsis` 是空目录，
 * `//go:embed` 直接编译失败）。新增的 Go 方法只能这样接进来，不碰 `bindings/`。
 *
 * ## 方法 ID 不是猜的
 *
 * 算法取自 wails v3.0.0-beta.12：
 *   - `pkg/application/bindings.go:245`  fqn := "<PkgPath>.<TypeName>.<MethodName>"
 *   - `pkg/application/bindings.go:251`  methodID := hash.Fnv(fqn)   （FNV-1a-32）
 *
 * 复算后与**已有 bindings 回归校验**：AdvancedIndexStatus / Close / Open /
 * PreviewImport / ImportFiles / ListChildren 六项 ID 全部吻合。
 *
 * ## 何时删除本文件
 *
 * 官方生成器可用后改用 `bindings/pvfine/services` 的生成结果并删除本文件。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.ImportProgress`。phase: scan / read / stage / index / install / done。 */
export interface ImportProgress {
  phase: string;
  scanned: number;
  total: number;
  bytes: number;
  running: boolean;
}

/**
 * CancelImport 请求取消正在进行的导入。
 * 取消点只在扫描/读取/写入暂存归档阶段 —— 这三步都在活动归档被改动之前，
 * 因此取消后归档与目录索引都保持原样。
 */
export function CancelImport(): $CancellablePromise<void> {
  return $Call.ByID(1864652228);
}

/** ImportStatus 返回当前（或最近一次）导入的进度。 */
export function ImportStatus(): $CancellablePromise<ImportProgress> {
  return $Call.ByID(2399014164);
}

/** 对应 Go `services.ImportResult`。 */
export interface ImportResult {
  targetDir: string;
  mode: string;
  importedCount: number;
  overwrittenCount: number;
  skippedCount: number;
  /** 文本导入中被自动改为「原始字节」写入的文件数（编码无法识别的二进制脚本）。 */
  autoRawCount?: number;
  changedPaths?: string[];
}

/**
 * ImportFilesEx 带冲突处理策略的导入（Go 侧新增方法，生成绑定尚未更新，走 ByName）。
 * conflict: "overwrite" 覆盖 | "rename" 重命名 | "skip" 跳过 | "abort" 终止。
 * fqn = pvfine/services.ArchiveService.ImportFilesEx
 */
export function ImportFilesEx(
  sourcePaths: string[],
  targetDir: string,
  mode: string,
  conflict: string
): $CancellablePromise<ImportResult> {
  return $Call.ByName("pvfine/services.ArchiveService.ImportFilesEx", sourcePaths, targetDir, mode, conflict);
}

/**
 * SelectImportFilesDialogFiles 原生多选文件对话框（不含文件夹；多文件夹用拖拽）。
 * fqn = pvfine/services.ArchiveService.SelectImportFilesDialogFiles
 */
export function SelectImportFilesDialogFiles(): $CancellablePromise<string[]> {
  return $Call.ByName("pvfine/services.ArchiveService.SelectImportFilesDialogFiles");
}

/** 对应 Go `services.LocalEntry`（文件夹选择器用，仅目录）。 */
export interface LocalEntry {
  name: string;
  path: string;
  isDir: boolean;
}

/**
 * ListLocalFiles 列出本地目录下的子目录 + 文件（目录在前）；path 为空返回盘符列表。
 * fqn = pvfine/services.ArchiveService.ListLocalFiles
 */
export function ListLocalFiles(path: string): $CancellablePromise<LocalEntry[]> {
  return $Call.ByName("pvfine/services.ArchiveService.ListLocalFiles", path);
}

/**
 * ListLocalDirectories 只列出本地目录（不含文件）；path 为空返回盘符列表。
 * 供「选择目标文件夹」这类只允许选目录的选择器使用。
 * fqn = pvfine/services.ArchiveService.ListLocalDirectories
 */
export function ListLocalDirectories(path: string): $CancellablePromise<LocalEntry[]> {
  return $Call.ByName("pvfine/services.ArchiveService.ListLocalDirectories", path);
}

/**
 * CreateLocalDirectory 在本地新建目录（含父级），返回创建后的路径。
 * fqn = pvfine/services.ArchiveService.CreateLocalDirectory
 */
export function CreateLocalDirectory(path: string): $CancellablePromise<string> {
  return $Call.ByName("pvfine/services.ArchiveService.CreateLocalDirectory", path);
}
