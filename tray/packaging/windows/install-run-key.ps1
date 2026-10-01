# Registra o WinCare na chave Run de HKLM (executar como administrador).
param([string]$Exe = "C:\Program Files\WinCare\wincare-tray.exe")
$key = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run"
Set-ItemProperty -Path $key -Name "WinCareTray" -Value "`"$Exe`" --hidden"
