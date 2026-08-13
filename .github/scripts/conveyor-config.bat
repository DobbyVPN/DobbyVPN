@echo off
python.exe "%~dp0desktop_build.py" conveyor-config
exit /b %ERRORLEVEL%
