import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import wails from "@wailsio/runtime/plugins/vite";

// 前端开发服务器端口：默认 9245（wails3 dev 用），HMR 热更新脚本会用 WAILS_VITE_PORT 覆盖。
const vitePort = Number(process.env.WAILS_VITE_PORT) || 9245;

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: vitePort,
    strictPort: true,
    // HMR 模式下页面从 http://wails.localhost:<port> 加载、由程序内部反向代理到本服务器，
    // 所以必须放行 wails.localhost（Vite 会 403 掉非白名单 Host）。
    allowedHosts: ["wails.localhost", "localhost", "127.0.0.1"],
    // Wails 只拦截 http://wails.localhost 的请求；ws:// 一律放行给 WebView2 直连，
    // 因此 HMR 的 WebSocket 必须指向 Vite 自己（127.0.0.1），不能跟随页面 origin。
    hmr: { protocol: "ws", host: "127.0.0.1", port: vitePort },
  },
  plugins: [vue(), wails("./bindings")],
});
