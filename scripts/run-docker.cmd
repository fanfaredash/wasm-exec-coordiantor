@echo off
setlocal enabledelayedexpansion

REM Windows CMD batch script to build and run the executor container.
REM Optional overrides before calling this file:
REM   set IMAGE=myrepo/executor:dev
REM   set SKIP_BUILD=1
REM   set SCENARIO=fib        (options: add|fib|affine)
REM   set ENTRY=fib           (override exported function manually)
REM   set ADD_X=10            (legacy add demo args)
REM   set ADD_Y=20

if "%IMAGE%"=="" set "IMAGE=executor-demo/executor:demo"
if "%SKIP_BUILD%"=="" set "SKIP_BUILD=0"
if "%ADD_X%"=="" set "ADD_X=5"
if "%ADD_Y%"=="" set "ADD_Y=7"
if "%SCENARIO%"=="" set "SCENARIO=add"

for %%I in ("%~dp0..") do set "ROOT=%%~fI"
set "HOST_DIR=%ROOT%\host"
set "WASM_DIR=%HOST_DIR%\wasm"
set "SHARED_DIR=%HOST_DIR%\shared"
set "EXAMPLE_DIR=%ROOT%\examples\wasm-tinygo"

set "WASM_BASENAME=module.wasm"
set "RESULT_BASENAME=result.json"
set "INPUT_BASENAME=input.json"
set "ENTRY_FROM_SCENARIO="
set "INPUT_TEMPLATE="

if /I "%SCENARIO%"=="fib" (
    set "WASM_BASENAME=fib.wasm"
    set "ENTRY_FROM_SCENARIO=fib"
    set "INPUT_TEMPLATE=%EXAMPLE_DIR%\fib\input.json"
    echo [*] Selected fib demo; expecting %WASM_DIR%\fib.wasm
) else if /I "%SCENARIO%"=="affine" (
    set "WASM_BASENAME=affine.wasm"
    set "ENTRY_FROM_SCENARIO=affine"
    set "INPUT_TEMPLATE=%EXAMPLE_DIR%\affine\input.json"
    echo [*] Selected affine demo; expecting %WASM_DIR%\affine.wasm
) else (
    set "SCENARIO=add"
)

set "WASM_FILE=%WASM_DIR%\%WASM_BASENAME%"
set "RESULT_FILE=%SHARED_DIR%\%RESULT_BASENAME%"
set "INPUT_FILE=%SHARED_DIR%\%INPUT_BASENAME%"
set "RUN_ENTRY=%ENTRY%"
if "%RUN_ENTRY%"=="" set "RUN_ENTRY=%ENTRY_FROM_SCENARIO%"

if not exist "%WASM_DIR%" (
    mkdir "%WASM_DIR%"
    if errorlevel 1 exit /b 1
)
if not exist "%SHARED_DIR%" (
    mkdir "%SHARED_DIR%"
    if errorlevel 1 exit /b 1
)

if not exist "%WASM_FILE%" (
    echo [!] Missing Wasm file: %WASM_FILE%
    echo     For add demo build host\wasm\module.wasm; for fib/affine place fib.wasm or affine.wasm.
    exit /b 1
)

if not "%INPUT_TEMPLATE%"=="" (
    if not exist "%INPUT_TEMPLATE%" (
        echo [!] Missing example input file: %INPUT_TEMPLATE%
        exit /b 1
    )
    echo [*] Copying input template to %INPUT_FILE%
    copy /Y "%INPUT_TEMPLATE%" "%INPUT_FILE%" >nul
)

if not exist "%RESULT_FILE%" (
    echo [*] Preparing output file %RESULT_FILE%
    type nul > "%RESULT_FILE%"
)

if /I "%SKIP_BUILD%"=="1" (
    echo [*] Skip docker build because SKIP_BUILD=%SKIP_BUILD%
) else (
    echo [*] Building image %IMAGE%...
    docker build -t "%IMAGE%" "%ROOT%"
    if errorlevel 1 exit /b 1
)

echo [*] Running container %IMAGE%...
if "%RUN_ENTRY%"=="" (
    docker run --rm ^
        -v "%HOST_DIR%:/data" ^
        -e WASM_PATH=/data/wasm/%WASM_BASENAME% ^
        -e OUTPUT_PATH=/data/shared/%RESULT_BASENAME% ^
        -e INPUT_PATH=/data/shared/%INPUT_BASENAME% ^
        -e ADD_X=%ADD_X% ^
        -e ADD_Y=%ADD_Y% ^
        "%IMAGE%"
) else (
    docker run --rm ^
        -v "%HOST_DIR%:/data" ^
        -e WASM_PATH=/data/wasm/%WASM_BASENAME% ^
        -e OUTPUT_PATH=/data/shared/%RESULT_BASENAME% ^
        -e INPUT_PATH=/data/shared/%INPUT_BASENAME% ^
        -e ADD_X=%ADD_X% ^
        -e ADD_Y=%ADD_Y% ^
        -e ENTRY=%RUN_ENTRY% ^
        "%IMAGE%"
)
if errorlevel 1 exit /b 1

if exist "%RESULT_FILE%" (
    echo.
    echo [*] Result file: %RESULT_FILE%
    type "%RESULT_FILE%"
)

endlocal
