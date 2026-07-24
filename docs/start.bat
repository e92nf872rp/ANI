@echo off
setlocal enabledelayedexpansion
chcp 65001 >nul 2>&1

REM ====================================================================
REM ANI Windows Startup Script (equivalent to repo/start.sh)
REM
REM Usage:
REM   start.bat             Default: start mock + console + boss, interactive
REM   start.bat setup       Initialize .env only
REM   start.bat build       Build Go binaries
REM   start.bat mock        Start Core API Mock only
REM   start.bat console     Start Console frontend only
REM   start.bat boss        Start BOSS frontend only
REM   start.bat gateway     Start ANI Gateway (requires build first)
REM   start.bat bg          Start mock + console + boss in background
REM   start.bat stop        Stop all services
REM   start.bat status      Show service status
REM   start.bat logs name   Tail logs for service (mock^|console^|boss^|gateway)
REM   start.bat help        Show this help
REM
REM Interactive commands: status | stop | start | quit | exit | help
REM On error, window stays open and prompts user to press any key.
REM ====================================================================

REM -- Paths and directories --
set SCRIPT_DIR=%~dp0
cd /d "%SCRIPT_DIR%..\repo" 2>nul || (
  echo [ERR] Cannot enter ..\repo directory
  pause
  exit /b 1
)

set RUN_DIR=%CD%\.run
set LOG_DIR=%CD%\.logs
set ENV_FILE=%CD%\.env
set ENV_EXAMPLE=%CD%\.env.example

if not exist "%RUN_DIR%" mkdir "%RUN_DIR%"
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"

REM -- ANSI colors (Win10+ supported) --
for /F %%a in ('echo prompt $E ^| cmd') do set "ESC=%%a"
set "C_GREEN=%ESC%[92m"
set "C_YELLOW=%ESC%[93m"
set "C_RED=%ESC%[91m"
set "C_BLUE=%ESC%[94m"
set "C_CYAN=%ESC%[96m"
set "C_RESET=%ESC%[0m"

goto :main

:log
echo %C_GREEN%[OK]%C_RESET% %~1
exit /b 0

:warn
echo %C_YELLOW%[WARN]%C_RESET% %~1
exit /b 0

:err
echo %C_RED%[ERR]%C_RESET% %~1
exit /b 0

:info
echo %C_BLUE%[INFO]%C_RESET% %~1
exit /b 0

:check_deps
set MISSING=
where go >nul 2>&1 || (
  call :err "Missing dependency: go"
  set MISSING=1
)
where node >nul 2>&1 || (
  call :err "Missing dependency: node"
  set MISSING=1
)
where python >nul 2>&1 || (
  call :err "Missing dependency: python"
  set MISSING=1
)
if defined MISSING (
  call :err "Please install: Go, Node, Python 3 first"
  exit /b 127
)
exit /b 0

:ensure_env
if exist "%ENV_FILE%" (
  call :log ".env already exists, skip copy"
  exit /b 0
)
if not exist "%ENV_EXAMPLE%" (
  call :warn ".env.example not found, creating empty .env"
  type nul > "%ENV_FILE%"
  exit /b 0
)
copy "%ENV_EXAMPLE%" "%ENV_FILE%" >nul
call :log "Copied .env from .env.example (local dev defaults)"
exit /b 0

:ensure_console_deps
if not exist "%CD%\frontends\console\node_modules" (
  call :info "First run: installing Console deps (npm install)..."
  pushd frontends\console
  call npm install
  set RC=!errorlevel!
  popd
  if not "!RC!"=="0" (
    call :err "Console npm install failed"
    exit /b 1
  )
)
exit /b 0

:ensure_boss_deps
if not exist "%CD%\frontends\boss\node_modules" (
  call :info "First run: installing BOSS deps (npm install)..."
  pushd frontends\boss
  call npm install
  set RC=!errorlevel!
  popd
  if not "!RC!"=="0" (
    call :err "BOSS npm install failed"
    exit /b 1
  )
)
exit /b 0

:load_env
if not exist "%ENV_FILE%" exit /b 0
for /f "usebackq eol=# tokens=1,* delims==" %%a in ("%ENV_FILE%") do (
  if not "%%a"=="" set "%%a=%%b"
)
exit /b 0

:start_bg
set BG_NAME=%~1
set BG_EXE=%~2
set BG_ARGS=
shift
shift
:build_args
if not "%~1"=="" (
  if defined BG_ARGS set BG_ARGS=!BG_ARGS!,
  set BG_ARGS=!BG_ARGS! '%~1'
  shift
  goto :build_args
)
set PIDFILE=%RUN_DIR%\%BG_NAME%.pid
set LOGFILE=%LOG_DIR%\%BG_NAME%.log
set ERRFILE=%LOG_DIR%\%BG_NAME%.log.err

if exist "%PIDFILE%" (
  for /f %%i in (%PIDFILE%) do set EXISTING_PID=%%i
  tasklist /FI "PID eq !EXISTING_PID!" 2>nul | findstr "!EXISTING_PID!" >nul && (
    call :warn "%BG_NAME% already running (PID !EXISTING_PID!)"
    exit /b 0
  )
  del "%PIDFILE%" 2>nul
)

call :info "Starting %BG_NAME% -> log: %LOGFILE% (stderr: %ERRFILE%)"
if defined BG_ARGS (
  powershell -NoProfile -Command "$p = Start-Process -FilePath '%BG_EXE%' -ArgumentList @(!BG_ARGS!) -PassThru -NoNewWindow -RedirectStandardOutput '%LOGFILE%' -RedirectStandardError '%ERRFILE%'; if ($p) { $p.Id | Out-File -Encoding ascii '%PIDFILE%' } else { '0' | Out-File -Encoding ascii '%PIDFILE%' }"
) else (
  powershell -NoProfile -Command "$p = Start-Process -FilePath '%BG_EXE%' -PassThru -NoNewWindow -RedirectStandardOutput '%LOGFILE%' -RedirectStandardError '%ERRFILE%'; if ($p) { $p.Id | Out-File -Encoding ascii '%PIDFILE%' } else { '0' | Out-File -Encoding ascii '%PIDFILE%' }"
)
set PS_RC=!errorlevel!
if not "!PS_RC!"=="0" (
  call :err "%BG_NAME% start failed (PowerShell Start-Process error)"
  exit /b 1
)
ping -n 3 127.0.0.1 >nul
if not exist "%PIDFILE%" (
  call :err "%BG_NAME% start failed, PID file not created"
  exit /b 1
)
for /f %%i in (%PIDFILE%) do set NEW_PID=%%i
if "!NEW_PID!"=="0" (
  call :err "%BG_NAME% start failed, check log: %LOGFILE%"
  type "%LOGFILE%" 2>nul
  exit /b 1
)
tasklist /FI "PID eq !NEW_PID!" 2>nul | findstr "!NEW_PID!" >nul && (
  call :log "%BG_NAME% started (PID !NEW_PID!)"
) || (
  call :err "%BG_NAME% start failed, check log: %LOGFILE%"
  type "%LOGFILE%" 2>nul
  exit /b 1
)
exit /b 0

:stop_bg
set SB_NAME=%~1
set PIDFILE=%RUN_DIR%\%SB_NAME%.pid
if not exist "%PIDFILE%" exit /b 0
for /f %%i in (%PIDFILE%) do set SB_PID=%%i
tasklist /FI "PID eq !SB_PID!" 2>nul | findstr "!SB_PID!" >nul && (
  call :info "Stopping %SB_NAME% (PID !SB_PID!)"
  taskkill /PID !SB_PID! /T /F >nul 2>&1
  call :log "%SB_NAME% stopped"
) || (
  call :warn "%SB_NAME% process not found, cleaning PID file"
)
del "%PIDFILE%" 2>nul
exit /b 0

:status_bg
echo NAME         PID      STATUS
for %%N in (mock console boss gateway) do (
  set SN=%%N
  set PIDFILE=%RUN_DIR%\!SN!.pid
  if exist "!PIDFILE!" (
    for /f %%i in (!PIDFILE!) do set SPID=%%i
    tasklist /FI "PID eq !SPID!" 2>nul | findstr "!SPID!" >nul && (
      echo !SN!         !SPID!       %C_GREEN%running%C_RESET%
    ) || (
      echo !SN!         !SPID!       %C_RED%dead%C_RESET%
    )
  ) else (
    echo !SN!         -          stopped
  )
)
exit /b 0

:stop_all
call :stop_bg mock
call :stop_bg console
call :stop_bg boss
call :stop_bg gateway
exit /b 0

:start_mock
where python >nul 2>&1 || (
  call :err "Missing dependency: python"
  exit /b 127
)
call :start_bg mock python "%CD%\scripts\serve_core_mock.py" --host 127.0.0.1 --port 4010
set RC=!errorlevel!
if not "!RC!"=="0" exit /b !RC!
echo.
call :info "Core API Mock Server: http://127.0.0.1:4010/api/v1"
exit /b 0

:start_console
where node >nul 2>&1 || (
  call :err "Missing dependency: node"
  exit /b 127
)
call :ensure_console_deps
set RC=!errorlevel!
if not "!RC!"=="0" exit /b !RC!
call :start_bg console cmd /c "npm --prefix %CD%\frontends\console run dev"
set RC=!errorlevel!
if not "!RC!"=="0" exit /b !RC!
echo.
call :info "Console UI: http://localhost:5173"
exit /b 0

:start_boss
where node >nul 2>&1 || (
  call :err "Missing dependency: node"
  exit /b 127
)
call :ensure_boss_deps
set RC=!errorlevel!
if not "!RC!"=="0" exit /b !RC!
call :start_bg boss cmd /c "npm --prefix %CD%\frontends\boss run dev"
set RC=!errorlevel!
if not "!RC!"=="0" exit /b !RC!
echo.
call :info "BOSS UI: http://localhost:5174"
exit /b 0

:start_gateway
if not exist "%CD%\bin\ani-gateway.exe" (
  call :err "bin\ani-gateway.exe not found, run: start.bat build first"
  exit /b 1
)
call :load_env
call :start_bg gateway "%CD%\bin\ani-gateway.exe"
set RC=!errorlevel!
if not "!RC!"=="0" exit /b !RC!
echo.
if defined GATEWAY_PORT (
  call :info "ANI Gateway: http://127.0.0.1:!GATEWAY_PORT!"
) else (
  call :info "ANI Gateway: http://127.0.0.1:8080"
)
call :warn "Gateway requires PostgreSQL / NATS / Redis to be ready"
call :warn "Use docker compose to start deps: docker compose -f deploy\docker\docker-compose.yml up -d"
exit /b 0

:show_banner
echo.
echo %C_CYAN%===============================================================%C_RESET%
echo %C_CYAN%  ANI dev stack started (logs in .logs directory)              %C_RESET%
echo   Core API Mock:  http://127.0.0.1:4010/api/v1
echo   Console UI:     http://localhost:5173
echo   BOSS UI:         http://localhost:5174
echo %C_CYAN%===============================================================%C_RESET%
echo.
echo %C_CYAN%  Interactive commands:%C_RESET%
echo %C_CYAN%    status  - show service status%C_RESET%
echo %C_CYAN%    stop    - stop all services (stay in shell)%C_RESET%
echo %C_CYAN%    start   - restart all services%C_RESET%
echo %C_CYAN%    quit    - stop all and exit%C_RESET%
echo %C_CYAN%    exit    - same as quit%C_RESET%
echo %C_CYAN%    help    - show help%C_RESET%
echo %C_CYAN%===============================================================%C_RESET%
echo.
exit /b 0

:show_interactive_help
echo.
echo %C_CYAN%Interactive commands:%C_RESET%
echo   status   - show service status
echo   stop     - stop all services
echo   start    - restart all services
echo   quit     - stop all and exit
echo   exit     - same as quit
echo   help     - show this help
echo.
exit /b 0

:cleanup_and_exit
echo.
call :warn "Exiting: stopping all services..."
call :stop_all
call :warn "Done"
exit /b 0

:interactive_loop
call :show_banner
:iloop
set LINE=
set /p LINE=ani^>
if errorlevel 1 goto :cleanup_and_exit
if /I "!LINE!"=="quit" goto :cleanup_and_exit
if /I "!LINE!"=="exit" goto :cleanup_and_exit
if /I "!LINE!"=="status" (
  echo.
  call :status_bg
  echo.
  goto :iloop
)
if /I "!LINE!"=="stop" (
  echo.
  call :warn "Stopping all services..."
  call :stop_all
  echo.
  goto :iloop
)
if /I "!LINE!"=="start" (
  echo.
  call :warn "Restarting all services..."
  call :stop_all
  call :start_mock
  call :start_console
  call :start_boss
  echo.
  goto :iloop
)
if /I "!LINE!"=="help" (
  call :show_interactive_help
  goto :iloop
)
if "!LINE!"=="" goto :iloop
call :err "Unknown command: !LINE! (type help)"
goto :iloop

:cmd_setup
call :check_deps
if errorlevel 1 goto :error_pause
call :ensure_env
call :log "Setup done (binaries not built, run: start.bat build)"
exit /b 0

:cmd_build
call :check_deps
if errorlevel 1 goto :error_pause
call :info "Building Go binaries (into %CD%\bin\)..."
if not exist "%CD%\bin" mkdir "%CD%\bin"
for %%P in ("services/ani-gateway:ani-gateway" "services/auth-service:auth-service" "services/model-service:model-service" "services/task-service:task-service" "services/reconcile-worker:reconcile-worker" "cli/ani:ani") do (
  for /f "tokens=1,* delims=:" %%a in ("%%P") do (
    set PKG=%%a
    set OUT=%%b
    call :info "  -^> building !OUT! (!PKG!)"
    pushd !PKG!
    set GOARCH=amd64
    set CGO_ENABLED=0
    go build -ldflags "-X main.Version=dev" -o "%CD%\bin\!OUT!.exe" .
    set RC=!errorlevel!
    popd
    if not "!RC!"=="0" (
      call :err "Build !OUT! failed"
      goto :error_pause
    )
  )
)
call :log "Go binaries built successfully"
exit /b 0

:cmd_default
call :check_deps
if errorlevel 1 goto :error_pause
call :ensure_env
call :start_mock
if errorlevel 1 goto :error_pause
call :start_console
if errorlevel 1 goto :error_pause
call :start_boss
if errorlevel 1 goto :error_pause
call :interactive_loop
exit /b 0

:cmd_bg
call :check_deps
if errorlevel 1 goto :error_pause
call :ensure_env
call :start_mock
if errorlevel 1 goto :error_pause
call :start_console
if errorlevel 1 goto :error_pause
call :start_boss
if errorlevel 1 goto :error_pause
echo.
call :log "Started mock + console + boss in background"
call :info "  - Core API Mock: http://127.0.0.1:4010/api/v1"
call :info "  - Console UI:    http://localhost:5173"
call :info "  - BOSS UI:       http://localhost:5174"
call :info "Stop: start.bat stop"
call :info "Logs: start.bat logs mock | start.bat logs console | start.bat logs boss"
exit /b 0

:cmd_stop
call :stop_all
call :log "All services stopped"
exit /b 0

:cmd_status
call :status_bg
exit /b 0

:cmd_logs
set LL_NAME=%~1
if "%LL_NAME%"=="" (
  call :err "Usage: start.bat logs ^<mock^|console^|boss^|gateway^>"
  exit /b 2
)
set LL_FILE=%LOG_DIR%\%LL_NAME%.log
if not exist "%LL_FILE%" (
  call :err "Log not found: %LL_FILE%"
  exit /b 1
)
call :info "Tailing %LL_NAME% log (Ctrl+C to exit)..."
powershell -NoProfile -Command "Get-Content -Path '%LL_FILE%' -Wait -Tail 50"
exit /b 0

:cmd_help
echo Usage: start.bat [command]
echo.
echo Commands:
echo   (default)  Start mock + console + boss, interactive mode
echo   setup      Initialize .env
echo   build      Build Go binaries
echo   mock       Start only Core API Mock
echo   console    Start only Console frontend
echo   boss       Start only BOSS frontend
echo   gateway    Start ANI Gateway (requires build first)
echo   bg         Start mock + console + boss in background
echo   stop       Stop all services
echo   status     Show service status
echo   logs name  Tail logs for service (mock^|console^|boss^|gateway)
echo   help       Show this help
echo.
echo Interactive commands (in default mode):
echo   status     Show service status
echo   stop       Stop all services
echo   start      Restart all services
echo   quit       Stop all and exit
echo   exit       Same as quit
echo   help       Show interactive help
exit /b 0

:error_pause
echo.
echo %C_RED%[ERR] Startup or build failed, press any key to close...%C_RESET%
pause >nul
exit /b 1

:main
set CMD=%~1
if "%CMD%"=="" set CMD=default

if /I "%CMD%"=="default" goto :cmd_default
if /I "%CMD%"=="all" goto :cmd_default
if /I "%CMD%"=="setup" (
  call :cmd_setup
  if errorlevel 1 goto :error_pause
  goto :eof
)
if /I "%CMD%"=="build" (
  call :cmd_build
  goto :eof
)
if /I "%CMD%"=="mock" (
  call :check_deps
  if errorlevel 1 goto :error_pause
  call :start_mock
  if errorlevel 1 goto :error_pause
  call :interactive_loop
  goto :eof
)
if /I "%CMD%"=="console" (
  call :check_deps
  if errorlevel 1 goto :error_pause
  call :start_console
  if errorlevel 1 goto :error_pause
  call :interactive_loop
  goto :eof
)
if /I "%CMD%"=="boss" (
  call :check_deps
  if errorlevel 1 goto :error_pause
  call :start_boss
  if errorlevel 1 goto :error_pause
  call :interactive_loop
  goto :eof
)
if /I "%CMD%"=="gateway" (
  call :check_deps
  if errorlevel 1 goto :error_pause
  call :start_gateway
  if errorlevel 1 goto :error_pause
  call :interactive_loop
  goto :eof
)
if /I "%CMD%"=="bg" (
  call :cmd_bg
  goto :eof
)
if /I "%CMD%"=="stop" (
  call :cmd_stop
  goto :eof
)
if /I "%CMD%"=="status" (
  call :cmd_status
  goto :eof
)
if /I "%CMD%"=="logs" (
  call :cmd_logs %~2
  if errorlevel 1 goto :error_pause
  goto :eof
)
if /I "%CMD%"=="help" goto :cmd_help
if /I "%CMD%"=="-h" goto :cmd_help
if /I "%CMD%"=="--help" goto :cmd_help

call :err "Unknown command: %CMD%"
call :cmd_help
exit /b 2
