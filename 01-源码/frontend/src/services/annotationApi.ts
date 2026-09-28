/**
 * 注释服务适配层（临时，与 objectViewApi.ts / doctorApi.ts 同因：本机无法构建
 * wails3 CLI 生成 bindings，所以新方法手写在这里，**不碰** `bindings/`）。
 *
 * 方法 ID = FNV-1a-32(`<PkgPath>.<TypeName>.<MethodName>`)：
 *   pvfine/services.AnnotationService.AnnotationSources → 3689293021
 *   pvfine/services.AnnotationService.ReloadRules       → 2871075508（与生成绑定一致，已回归验证）
 */
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `annotations.ExternalSummary`。 */
export interface ExternalAnnotationSummary {
  dir: string;
  found: boolean;
  durationMs: number;
  fieldFiles: number;
  fields: number;
  rules: number;
  pathRules: number;
  hoverRules: number;
  relations: number;
  warnings?: string[] | null;
}

/** 对应 Go `services.AnnotationReloadResult`。 */
export interface AnnotationReloadResult {
  ruleCount: number;
  relationCount: number;
  durationMs: number;
  external: ExternalAnnotationSummary;
}

/** 当前生效的注释来源与规模（只读，不触发重新加载）。 */
export function AnnotationSources(): $CancellablePromise<AnnotationReloadResult> {
  return $Call.ByID(3689293021);
}

/** 重新加载注释（内置基础注解 + 外置「注释数据」目录）。 */
export function ReloadRules(): $CancellablePromise<AnnotationReloadResult> {
  return $Call.ByID(2871075508);
}
