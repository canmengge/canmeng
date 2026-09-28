/**
 * 归档体检服务的调用适配层（临时）。
 *
 * 与 `objectViewApi.ts` / `stringGuardApi.ts` 同因：本机无法构建 wails3 CLI
 * （模块缓存里 `internal/commands/build_assets/windows/{msix,nsis}` 为空目录，
 * `//go:embed` 编译失败），所以 `frontend/bindings/` 生成不了新服务的绑定，
 * 这里用一个独立适配层，**不碰** `bindings/`。
 *
 * 方法 ID 不是猜的：fqn = `<PkgPath>.<TypeName>.<MethodName>`，methodID = FNV-1a-32(fqn)。
 *   fqn = pvfine/services.DoctorService.Run → 1754185643
 * 已用仓库内既有绑定 ID 回归验证 3/3 吻合
 * （ObjectViewService.ListObjectTypes / ResolveObject / ReloadRules）。
 */
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.DoctorExtension`。 */
export interface DoctorExtension {
  ext: string;
  count: number;
  bytes: number;
}

/** 对应 Go `services.DoctorTableCheck`。 */
export interface DoctorTableCheck {
  tableIndex: number;
  path: string;
  protected: boolean;
  exists: boolean;
  size: number;
  sha256: string;
}

/** 对应 Go `services.DoctorTableSummary`。 */
export interface DoctorTableSummary {
  total: number;
  present: number;
  missing: number;
  protected: number;
}

/** 对应 Go `services.DoctorCoverage`。 */
export interface DoctorCoverage {
  ext: string;
  count: number;
  samples: string[];
}

/** 对应 Go `services.DoctorListCoverage`。 */
export interface DoctorListCoverage {
  listPaths: string[];
  registered: number;
  checked: number;
  unregistered: number;
}

/** 对应 Go `services.DoctorReport`。 */
export interface DoctorReport {
  path: string;
  generatedAt: string;
  durationMs: number;
  fileCount: number;
  dirCount: number;
  groupCount: number;
  bodySize: number;
  paged110: boolean;
  extensions: DoctorExtension[];
  tables: DoctorTableCheck[];
  tableSummary: DoctorTableSummary;
  listCoverage: DoctorListCoverage;
  unregisteredTop: DoctorCoverage[];
  warnings: string[];
}

/** 执行一次只读体检（同一时刻只允许一个）。 */
export function Run(): $CancellablePromise<DoctorReport> {
  return $Call.ByID(1754185643);
}
