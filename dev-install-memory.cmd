@echo off
REM  dev-install-memory.cmd
REM  Wipes and rebuilds the soulman-dev memory environment, then runs the builder
REM  agent non-interactively. Safe for pipelines.
REM
REM  Prerequisites:
REM    - supabase start must be running (API at localhost:54321)
REM    - opencode must be installed and on PATH
REM    - curl must be available (Windows 10+ includes it)

setlocal enabledelayedexpansion

set "DEV=%USERPROFILE%\soulman-dev"
set "VAULT=%USERPROFILE%\Documents\obsidian\brynjar-obsidian"

echo === dev-install-memory ===

REM -- Step 1: Verify supabase is running --------------------------
echo.
echo [1/5] Checking supabase...
curl -s -o nul http://127.0.0.1:54321/mcp
if errorlevel 1 (
    echo ERROR: supabase is not reachable at http://127.0.0.1:54321/mcp
    echo        Run "supabase start" and try again.
    exit /b 1
)
echo        OK -- supabase is running

REM -- Step 2: Wipe dev directory ---------------------------------
echo.
echo [2/5] Wiping %DEV% ...
if exist "%DEV%" (
    rmdir /s /q "%DEV%" 2>nul
    if exist "%DEV%" (
        echo ERROR: could not remove %DEV%
        echo        Close any open windows or processes using it.
        exit /b 1
    )
)
echo        Done

REM -- Step 3: Create directory structure --------------------------
echo.
echo [3/5] Creating directory structure...
mkdir "%DEV%\memory\.opencode\agent"
if errorlevel 1 (echo ERROR: mkdir failed & exit /b 1)
mkdir "%DEV%\memory\logs"
if errorlevel 1 (echo ERROR: mkdir failed & exit /b 1)
echo        %DEV%\memory\.opencode\agent
echo        %DEV%\memory\logs

REM -- Step 4: Sync files from vault -------------------------------
echo.
echo [4/5] Copying files from vault...

copy "%VAULT%\memory\Implementation Plan.md"  "%DEV%\memory\plan.md" /Y >nul
if errorlevel 1 ( echo ERROR: failed to copy plan.md & exit /b 1 )

copy "%VAULT%\Memory module.md"               "%DEV%\memory\design.md" /Y >nul
if errorlevel 1 ( echo ERROR: failed to copy design.md & exit /b 1 )

copy "%VAULT%\memory\CLAUDE.md"               "%DEV%\memory\CLAUDE.md" /Y >nul
if errorlevel 1 ( echo ERROR: failed to copy CLAUDE.md & exit /b 1 )

xcopy "%VAULT%\memory\.opencode\agent\*.md"   "%DEV%\memory\.opencode\agent\" /Y /Q >nul
if errorlevel 1 ( echo ERROR: failed to copy agent definitions & exit /b 1 )

copy "%VAULT%\memory\opencode.json"           "%DEV%\memory\opencode.json" /Y >nul
if errorlevel 1 ( echo ERROR: failed to copy opencode.json & exit /b 1 )

echo        plan.md               --^> memory\
echo        design.md             --^> memory\
echo        CLAUDE.md             --^> memory\
echo        .opencode\agent\*.md  --^> memory\.opencode\agent\
echo        opencode.json         --^> memory\

REM -- Step 5: Run the builder agent -------------------------------
echo.
echo [5/5] Running soulman-db-builder...
echo        cd %DEV%\memory
echo        opencode --agent soulman-db-builder run "Read plan.md, then execute steps 0 through 10"
echo.

cd /d "%DEV%\memory"
echo        CWD: %CD%
echo.

opencode --agent soulman-db-builder run "Read plan.md, then execute steps 0 through 10"

if errorlevel 1 (
    echo.
    echo === BUILD FAILED ===
    echo Check the output above for errors from the builder agent.
    exit /b 1
)

echo.
echo === dev-install-memory complete ===
echo Schema memory_dev should now be live on localhost:54322.
echo.
echo To verify:
echo   cd %DEV%\memory ^&^& opencode --agent soulman-db-builder run "@soulman-db-retrieve list all tables in memory_dev"
