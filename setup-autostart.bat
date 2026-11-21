@echo off
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -Command "$dir=[IO.Path]::Combine($env:APPDATA,'Microsoft\Windows\Start Menu\Programs\Startup'); $s=(New-Object -ComObject WScript.Shell).CreateShortcut([IO.Path]::Combine($dir,'clipshare.lnk')); $s.TargetPath='C:\ProgramData\clipshare\clipshare.exe'; $s.WorkingDirectory='C:\ProgramData\clipshare'; $s.Save()"
start "" "C:\ProgramData\clipshare\clipshare.exe"
echo clipshare started, autostart registered in your Startup folder.
pause
