@echo off
setlocal
REM ============================================================================
REM  PVF工坊 - HMR 热更新模式（唯一日常启动入口，2026-09-28 用户规定）
REM  - 以后更新一律只用本脚本；其它启动脚本仅在 HMR 不可用时作为回退
REM  - 只改前端源码：保存即热更新（约 1 秒），不构建、不生成 exe
REM  - 只有改了 Go（main.go / services / internal）才需要重新构建 exe
REM   1) 起前端开发服务器（另开一个窗口，别关它）；启动前会清理上次残留
REM   2) 以「外部开发服务器」模式启动程序：界面资源实时取源码，不走内嵌资源
REM   3) 之后凡是改前端源码 → 保存 → 程序界面约 1 秒自动更新
REM  - 后端接口（打开 PVF / 保存 / 索引 / 注释 / AI）仍由程序自身处理，不受影响
REM ============================================================================
set "HERE=%~dp0"
for %%I in ("%HERE%..") do set "CLIENT=%%~fI"
for %%I in ("%HERE%..\..") do set "ROOT=%%~fI"
set "FE=%ROOT%\01-源码\frontend"
set "PORT=9255"

if not exist "%FE%\package.json" (
  echo [错误] 找不到前端源码目录：%FE%
  echo        本脚本必须放在 04-运行环境\HMR 热更新\ 下
  pause
  exit /b 1
)

echo ------------------------------------------------------------
echo  工作台根目录: %ROOT%
echo  前端源码目录: %FE%
echo  日志目录:     %CLIENT%\日志
if /i not "%ROOT%"=="d:\110AI" echo  [警告] 当前不是 d:\110AI 工作台，请确认是否双击了旧目录的脚本
echo ------------------------------------------------------------

REM ---- 自愈（2026-09-28 加）--------------------------------------------------
REM 关闭窗口时 node 常不随之退出，残留进程会占住 %PORT%，使开发服务器窗口报
REM "Port 9255 is already in use"。这里先清掉所有占用者，再启动干净的新实例，
REM 保证每次双击都能起来、且窗口可见（旧实例的窗口此时已找不回来）。
for /f "tokens=5" %%a in ('netstat -ano ^| findstr /c:"127.0.0.1:%PORT%" ^| findstr /i "LISTENING"') do (
  echo [自愈] 清理占用 %PORT% 的残留进程 PID %%a
  taskkill /F /PID %%a >nul 2>&1
)
timeout /t 1 /nobreak >nul

echo === 1/3 启动前端开发服务器（另开窗口，请勿关闭）===
start "PVF工坊-前端开发服务器(勿关)" "%HERE%_前端开发服务器.cmd"

echo === 2/3 等待开发服务器就绪 127.0.0.1:%PORT% ...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$end=(Get-Date).AddSeconds(180); while((Get-Date) -lt $end){ try { $c=New-Object Net.Sockets.TcpClient; $c.Connect('127.0.0.1',%PORT%); $c.Close(); exit 0 } catch { Start-Sleep -Milliseconds 400 } }; exit 1"
if errorlevel 1 (
  echo [错误] 开发服务器 180 秒内未就绪，请看「前端开发服务器」窗口的报错信息
  pause
  exit /b 1
)

echo === 3/3 启动 PVF工坊（热更新模式）===
set "FRONTEND_DEVSERVER_URL=http://localhost:%PORT%"
set "PVFINE_ANNOTATION_DIR=%CLIENT%\注释数据"
set "PVFINE_CACHE_DIR=%CLIENT%\pvfine-main\HC"
set "PVFINE_KNOWLEDGE_DIR=%CLIENT%\知识库"
REM 日志固定落在 04-运行环境\日志（与缓存/索引目录分离，便于直接查看）
set "PVFINE_LOG_DIR=%CLIENT%\日志"
if not exist "%CLIENT%\日志" mkdir "%CLIENT%\日志"

set "EXE="
for /f "delims=" %%F in ('dir /b /o-d "%CLIENT%\PVF工坊-开发人员专用*.exe" 2^>nul') do (
  if not defined EXE set "EXE=%CLIENT%\%%F"
)
if not defined EXE set "EXE=%ROOT%\05-构建输出\pvfine-devkit.exe"
if not exist "%EXE%" (
  echo [错误] 找不到程序：%EXE%
  pause
  exit /b 1
)
echo 启动程序: %EXE%
start "" "%EXE%"
endlocal
