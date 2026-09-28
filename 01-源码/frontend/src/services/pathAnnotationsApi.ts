import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

// 手写绑定（与 aiApi.ts 同策略）：服务方法少、避免为了三个方法跑一次代码生成。
// fqn 前缀 = "pvfine/services.PathAnnotationService"，见 services/pathannotations.go。
const FQN = "pvfine/services.PathAnnotationService";

/** 对应 Go `services.PathAnnotationOverride`。 */
export interface PathAnnotationOverride {
  title: string;
  content?: string;
  updatedAt?: string;
}

/** 对应 Go `services.PathAnnotationResult`。 */
export interface PathAnnotationResult {
  found: boolean;
  override?: PathAnnotationOverride | null;
}

/** 查询单个路径的用户注释（found=false 表示走的是内置注释）。 */
export function GetPathAnnotation(path: string): $CancellablePromise<PathAnnotationResult> {
  return $Call.ByName(`${FQN}.Get`, path);
}

/** 写入用户注释（title 为空 = 删除，回退内置注释）。 */
export function SetPathAnnotation(
  path: string,
  title: string,
  content: string
): $CancellablePromise<void> {
  return $Call.ByName(`${FQN}.Set`, path, title, content);
}

/** 删除用户注释（该路径回退到内置注释规则）。 */
export function RemovePathAnnotation(path: string): $CancellablePromise<void> {
  return $Call.ByName(`${FQN}.Remove`, path);
}
