<#
.SYNOPSIS
    Módulo office: Office, Teams, OneDrive e SharePoint (origem: WinCare Pro, módulo 04).
    Sem interface, roda como SYSTEM. Pastas de usuário são tratadas em todos os perfis de
    C:\Users; ações que precisam da sessão do usuário rodam via tarefa agendada temporária.
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

function Clear-WCProfileDirs {
    param([string[]]$Relative)
    foreach ($p in Get-WCUserProfiles) {
        foreach ($rel in $Relative) {
            $dir = Join-Path $p.Path $rel
            if (Test-Path -LiteralPath $dir) {
                $ct = Clear-WCDirectory -Path $dir -Root $p.Path
                Write-Log "  Limpo ($ct itens): $dir" -Level SUCCESS
            }
        }
    }
}

function Get-WCC2RClient {
    $c2r   = "${env:ProgramFiles}\Common Files\microsoft shared\ClickToRun\OfficeC2RClient.exe"
    $c2r86 = "${env:ProgramFiles(x86)}\Common Files\microsoft shared\ClickToRun\OfficeC2RClient.exe"
    if (Test-Path -LiteralPath $c2r) { return $c2r }
    if ($c2r86 -and (Test-Path -LiteralPath $c2r86)) { return $c2r86 }
    return $null
}

$WCTasks = [ordered]@{

    'office_repair' = {
        Write-Log "Reparo online do Office..."
        $c2rP = Get-WCC2RClient
        if (-not $c2rP) { Write-Log "OfficeC2RClient não encontrado" -Level WARN; return }
        $code = Start-WCProcess -FilePath $c2rP -ArgumentList '/repair producttype=O365ProPlusRetail DisplayLevel=False' -TimeoutMinutes 90
        Write-Log "Reparo online concluído (código $code)" -Level SUCCESS
    }

    'office_quick_repair' = {
        Write-Log "Reparo rápido do Office..."
        $c2rP = Get-WCC2RClient
        if (-not $c2rP) { Write-Log "OfficeC2RClient não encontrado" -Level WARN; return }
        $code = Start-WCProcess -FilePath $c2rP -ArgumentList '/repair producttype=O365ProPlusRetail quickrepair DisplayLevel=False' -TimeoutMinutes 60
        Write-Log "Reparo rápido concluído (código $code)" -Level SUCCESS
    }

    'office_activation' = {
        Write-Log "Verificando a ativação do Office..."
        $ospp = @("${env:ProgramFiles}\Microsoft Office\Office16\ospp.vbs",
                  "${env:ProgramFiles(x86)}\Microsoft Office\Office16\ospp.vbs") |
            Where-Object { $_ -and (Test-Path -LiteralPath $_) } | Select-Object -First 1
        if ($ospp) { & cscript //nologo $ospp /dstatus 2>&1 | ForEach-Object { Write-Log "  $_" } }
        else { Write-Log "ospp.vbs não encontrado" -Level WARN }
    }

    'office_credentials' = {
        Write-Log "Limpando tokens MSAL do Office..."
        Clear-WCProfileDirs @('AppData\Local\Microsoft\Office\16.0\Licensing',
                              'AppData\Roaming\Microsoft\Office\16.0\Common\Identity',
                              'AppData\Local\Microsoft\IdentityCache')
        # O cofre de credenciais é por usuário: cmdkey precisa rodar na sessão dele.
        $cmd = @'
& cmdkey /list 2>&1 | Select-String "MicrosoftOffice|microsoftoffice" | ForEach-Object {
    $tgt = ($_ -replace '.*Target: ', '' -replace '.*Destino: ', '').Trim()
    & cmdkey "/delete:$tgt" 2>&1 | Out-Null
}
'@
        Invoke-WCAsLoggedOnUser -Command $cmd -TimeoutSeconds 120
        Write-Log "Credenciais do Office removidas" -Level SUCCESS
    }

    'office_cache' = {
        Write-Log "Limpando o cache do Office..."
        Clear-WCProfileDirs @('AppData\Local\Microsoft\Office\16.0\OfficeFileCache',
                              'AppData\Local\Microsoft\Office\OTelemetry',
                              'AppData\Local\Microsoft\Office\16.0\Lync\Tracing')
    }

    'teams_classic_cache' = {
        Write-Log "Limpando o cache do Teams clássico..."
        Stop-Process -Name Teams -Force -ErrorAction SilentlyContinue; Start-Sleep 2
        $base = 'AppData\Roaming\Microsoft\Teams'
        Clear-WCProfileDirs @("$base\Cache", "$base\blob_storage", "$base\databases", "$base\GPUCache",
                              "$base\IndexedDB", "$base\Local Storage", "$base\tmp", "$base\Service Worker\CacheStorage")
        Write-Log "Cache do Teams clássico limpo" -Level SUCCESS
    }

    'teams_new_cache' = {
        Write-Log "Limpando o cache do novo Teams..."
        Stop-Process -Name ms-teams -Force -ErrorAction SilentlyContinue; Start-Sleep 2
        Clear-WCProfileDirs @('AppData\Local\Packages\MSTeams_8wekyb3d8bbwe\LocalCache\Microsoft\MSTeams')
        Write-Log "Cache do novo Teams limpo" -Level SUCCESS
    }

    'teams_reregister' = {
        Write-Log "Registrando o Teams novamente..."
        Get-AppxPackage -AllUsers -Name *MSTeams* -ErrorAction SilentlyContinue | ForEach-Object {
            Add-AppxPackage -DisableDevelopmentMode -Register "$($_.InstallLocation)\AppXManifest.xml" -ErrorAction SilentlyContinue
        }
        Write-Log "Teams registrado novamente" -Level SUCCESS
    }

    'onedrive_cache' = {
        Write-Log "Limpando o cache do OneDrive..."
        Clear-WCProfileDirs @('AppData\Local\Microsoft\OneDrive\logs', 'AppData\Local\Microsoft\OneDrive\setup\logs')
    }

    'onedrive_reset' = {
        Write-Log "Redefinindo o OneDrive..."
        $cmd = @'
$od = Join-Path $env:LOCALAPPDATA 'Microsoft\OneDrive\OneDrive.exe'
if (-not (Test-Path $od)) { $od = Join-Path $env:ProgramFiles 'Microsoft OneDrive\OneDrive.exe' }
if (Test-Path $od) {
    Stop-Process -Name OneDrive -Force -ErrorAction SilentlyContinue; Start-Sleep 3
    Start-Process $od -ArgumentList '/reset' -Wait -ErrorAction SilentlyContinue
    Start-Sleep 5; Start-Process $od -ErrorAction SilentlyContinue
}
'@
        Invoke-WCAsLoggedOnUser -Command $cmd -TimeoutSeconds 300
        Write-Log "OneDrive redefinido" -Level SUCCESS
    }

    'onedrive_reinstall' = {
        Write-Log "Reinstalando o OneDrive..."
        Stop-Process -Name OneDrive -Force -ErrorAction SilentlyContinue; Start-Sleep 2
        $ods = if (Test-Path "$env:SystemRoot\SysWOW64\OneDriveSetup.exe") { "$env:SystemRoot\SysWOW64\OneDriveSetup.exe" }
               else { "$env:SystemRoot\System32\OneDriveSetup.exe" }
        if (-not (Test-Path -LiteralPath $ods)) { Write-Log "OneDriveSetup.exe não encontrado" -Level WARN; return }
        $cmd = "Start-Process '$ods' -ArgumentList '/uninstall' -Wait; Start-Sleep 3; Start-Process '$ods' -Wait"
        Invoke-WCAsLoggedOnUser -Command $cmd -TimeoutSeconds 900
        Write-Log "OneDrive reinstalado" -Level SUCCESS
    }

    'sharepoint_cache' = {
        Write-Log "Limpando o cache do SharePoint..."
        Clear-WCProfileDirs @('AppData\Local\Microsoft\SharePoint')
    }

    'sharepoint_resync' = {
        Write-Log "Sincronizando o SharePoint novamente..."
        Stop-Process -Name OneDrive -Force -ErrorAction SilentlyContinue; Start-Sleep 2
        $cmd = @'
$od = Join-Path $env:LOCALAPPDATA 'Microsoft\OneDrive\OneDrive.exe'
if (-not (Test-Path $od)) { $od = Join-Path $env:ProgramFiles 'Microsoft OneDrive\OneDrive.exe' }
if (Test-Path $od) { Start-Process $od -ErrorAction SilentlyContinue }
'@
        Invoke-WCAsLoggedOnUser -Command $cmd -NoWait
        Write-Log "Nova sincronização iniciada" -Level SUCCESS
    }

    'sharepoint_clear_libraries' = {
        Write-Log "Removendo atalhos de bibliotecas do SharePoint..."
        foreach ($p in Get-WCUserProfiles) {
            Get-Item -Path (Join-Path $p.Path 'OneDrive*\*') -Force -ErrorAction SilentlyContinue |
                Where-Object { $_.Attributes -match 'ReparsePoint' } |
                ForEach-Object {
                    # Remove só o link, nunca o conteúdo de destino.
                    try { [void](Remove-WCItemSafe -Path $_.FullName); Write-Log "  Removido: $($_.FullName)" -Level SUCCESS }
                    catch { Write-Log "  Falha ao remover $($_.FullName): $_" -Level WARN }
                }
        }
    }
}

Invoke-WCTasks -Tasks $WCTasks
