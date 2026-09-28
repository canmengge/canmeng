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

REM ---- 自愈（2026-09-28 加，2026-09-29 扩展）----------------------------------
REM 两类残留都要清掉：
REM   ① 关闭窗口时 node 常不随之退出，残留进程占住 %PORT% ? 新 dev server 报
REM      "Port 9255 is already in use"。
REM   ② 上一次的 dev server 已退出、它的 cmd 窗口却卡在 pause 上，成了「僵尸窗口」
REM      —— 用户会看到两个窗口，其中一个写着「开发服务器已退出 / 请按任意键继续」。
REM 清理逻辑统一放在 kill-stale-devserver.ps1（独立成文件，避免 cmd 里的转义问题）。
set "KILLED="
for /f "delims=" %%T in ('powershell -NoProfile -ExecutionPolicy Bypass -File "%HERE%kill-stale-devserver.ps1"') do set "KILLED=%%T"
if defined KILLED echo [自愈] 残留清理: %KILLED%
timeout /t 1 /nobreak >nul

REM ---- Go 源码 / exe 一致性自检（2026-09-29 加）-------------------------------
REM 本脚本只挑「文件时间最新」的 exe 启动，从不重新编译 Go。若改了 Go 却没先
REM 重新构建，跑的仍是【旧内核】——用户会误以为修复无效、白跑一轮（2026-09-29
REM 真实事故）。这里只【警告】不自动构建：自动构建一旦编译失败，用户就直接起不
REM 来程序了。判断逻辑在 check-exe-stale.ps1（独立成文件，避免 cmd 转义问题）。
set "PVF_CHK_SRC=%ROOT%\01-源码"
set "PVF_CHK_EXE_DIR=%CLIENT%"
set "PVF_CHK_EXE_PAT=PVF工坊-开发人员专用*.exe"
set "PVFSTALE="
for /f "delims=" %%T in ('powershell -NoProfile -ExecutionPolicy Bypass -File "%HERE%check-exe-stale.ps1"') do set "PVFSTALE=%%T"
if /i "%PVFSTALE%"=="STALE" (
  echo.
  echo ************************************************************
  echo  [警告] Go 源码比最新 exe 更新 —— 当前启动的是【旧内核】！
  echo.
  echo    若你刚改过 Go（main.go / services / internal），
  echo    请先双击:  03-脚本\构建开发版.cmd
  echo    再重新双击本脚本；否则本次跑的还是上一次编译的旧代码。
  echo ************************************************************
  echo.
  timeout /t 6 /nobreak >nul
)

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
