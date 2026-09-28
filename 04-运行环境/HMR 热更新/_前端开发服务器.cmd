@echo off
setlocal
REM ============================================================================
REM  前端开发服务器（由「启动HMR热更新.cmd」自动调用，一般不用手点）
REM  - 监听 127.0.0.1:9255，为程序提供界面资源并推送热更新
REM  - 关闭本窗口 = 停止热更新（程序会退回到内置界面资源）
REM  - 2026-09-28 加自愈：启动前清掉占用 9255 的残留进程，避免出现
REM    "Port 9255 is already in use"（直接双击本脚本时同样有效）
REM ============================================================================
for %%I in ("%~dp0..\..") do set "ROOT=%%~fI"
set "FE=%ROOT%\01-源码\frontend"

if not exist "%FE%\package.json" (
  echo [错误] 找不到前端源码：%FE%
  pause
  exit /b 1
)

REM ---- 自愈：清掉占用 9255 的残留进程 ----
for /f "tokens=5" %%a in ('netstat -ano ^| findstr /c:"127.0.0.1:9255" ^| findstr /i "LISTENING"') do (
  echo [自愈] 清理占用 9255 的残留进程 PID %%a
  taskkill /F /PID %%a >nul 2>&1
)
timeout /t 1 /nobreak >nul

set "WAILS_VITE_PORT=9255"
set "VITE_DEV_TOOLS=1"
cd /d "%FE%" || (
  echo [错误] 无法进入目录：%FE%
  pause
  exit /b 1
)

echo ============================================================
echo  前端开发服务器  http://127.0.0.1:9255
echo  改完源码保存即自动热更新到程序界面，无需构建、无需重启程序
echo  本窗口请保持打开
echo ============================================================
call npm run dev
echo.
echo [开发服务器已退出]
pause
endlocal
