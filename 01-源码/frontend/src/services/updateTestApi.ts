/**
 * 开发者面板专用的「更新通道测试」适配层（2026-09-29 新增，与 `importApi.ts` /
 * `keyApi.ts` 同源做法：走 `$Call.ByName`，不碰 `frontend/bindings/`）。
 *
 * ## 背景
 *
 * 设置里的「检查更新」在**开发版**下依旧是老行为 —— 框架更新器未初始化，点了会提示
 * 「更新功能未初始化」（用户 2026-09-29 明确要求保持不变）。为了在开发期验证
 * 「服务器是否可达、更新清单是否可读」，另开两个只给开发者面板用的入口：
 *
 * | 方法 | 干什么 |
 * | --- | --- |
 * | `TestCheckUpdateNow()` | **真实**请求当前通道的更新清单并比较版本；发现新版本同样会弹「提示更新」窗 |
 * | `SimulateUpdateAvailable()` | **不发网络请求**，直接模拟"发布了新版本"，走同一条事件链路弹窗 |
 *
 * 两者都不依赖框架更新器，因此开发版也能跑。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.UpdateInfo`。 */
export interface UpdateInfo {
  currentVersion: string;
  latestVersion: string;
  hasUpdate: boolean;
  downloadUrl: string;
}

/**
 * 真实请求当前更新通道的清单并比较版本（不依赖框架更新器）。
 * Go 方法：`pvfine/services.UpdateService.TestCheckUpdateNow`
 */
export function TestCheckUpdateNow(): $CancellablePromise<UpdateInfo> {
  return $Call.ByName("pvfine/services.UpdateService.TestCheckUpdateNow");
}

/**
 * 模拟"发布了新版本"，让界面弹出「提示更新」窗口（不走网络）。
 * Go 方法：`pvfine/services.UpdateService.SimulateUpdateAvailable`
 */
export function SimulateUpdateAvailable(): $CancellablePromise<UpdateInfo> {
  return $Call.ByName("pvfine/services.UpdateService.SimulateUpdateAvailable");
}
