@echo off
setlocal enabledelayedexpansion

for %%I in ("%~dp0..") do set "ROOT=%%~fI"
set "JOB=%ROOT%\k8s\job.yaml"

kubectl apply -f "%JOB%"
if errorlevel 1 (
  exit /b %errorlevel%
)

echo [*] Applied job. Use scripts\tail-k8s.sh to watch logs.

endlocal
