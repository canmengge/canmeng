/**
 * 密钥库（sk.dat）相关新增调用的适配层，与 `importApi.ts` / `objectViewApi.ts` 同源做法。
 *
 * ## 背景（2026-09-29）
 *
 * 编辑器已**内置两份 sk.dat**（新 / 旧），绝大多数 PVF 开箱即开；出现第三套密钥时
 * 不必改代码、不必重新发包 —— 把配套的 `sk.dat` 丢进「密钥库」目录即可：
 *
 *   打开 PVF 的密钥尝试顺序：
 *     ① 内置两份（新 → 旧）
 *     ② 密钥库目录下的全部密钥文件      ← 新密钥丢这里即生效
 *     ③ PVF 同目录的 sk.dat             ← 原有方式；命中后自动收藏进 ②
 *
 * 「打开失败 · 密钥不匹配」弹窗里的「选择 sk.dat 文件…」按钮走本文件的
 * `PickKeyFileDialog()`：选一次就入库，之后永久可用。
 *
 * ## 为什么不放 frontend/bindings/
 *
 * 同 `importApi.ts`：`bindings/` 由生成器产出、禁止手改，本机无法构建 wails3 CLI，
 * 因此新增的 Go 方法统一走 `$Call.ByName`。
 */

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore: Unused imports
import { Call as $Call, CancellablePromise as $CancellablePromise } from "@wailsio/runtime";

/** 对应 Go `services.KeyStoreInfo`。 */
export interface KeyStoreInfo {
  /** 密钥库目录（缓存目录下的 keys/；放进去的 sk.dat 会被自动尝试）。 */
  dir: string;
  /** 库里已有的密钥文件名。 */
  files: string[];
}

/**
 * KeyStoreInfo 返回密钥库目录与其中已有的密钥文件名。
 * Go 方法：`pvfine/services.ArchiveService.KeyStoreInfo`
 */
export function GetKeyStoreInfo(): $CancellablePromise<KeyStoreInfo> {
  return $Call.ByName("pvfine/services.ArchiveService.KeyStoreInfo");
}

/**
 * PickKeyFileDialog 弹出文件对话框选一个 sk.dat 并复制进密钥库。
 * 返回一句可展示的提示文案；用户取消时返回空字符串。
 * Go 方法：`pvfine/services.ArchiveService.PickKeyFileDialog`
 */
export function PickKeyFileDialog(): $CancellablePromise<string> {
  return $Call.ByName("pvfine/services.ArchiveService.PickKeyFileDialog");
}
