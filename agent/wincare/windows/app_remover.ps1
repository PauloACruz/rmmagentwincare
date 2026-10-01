<#
.SYNOPSIS
    Módulo app_remover: Desinstalador avançado e bloatware (origem: WinCare Pro, módulo 06).
    Sem interface, roda como SYSTEM. A seleção interativa do original virou as tarefas
    list e list_bloatware (evento result) e uninstall com o parâmetro apps.
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

$WCBloatware = @('BingNews', 'BingSports', 'BingWeather', 'BingFinance', '3DBuilder', 'Candy', 'Solitaire',
    'Xbox', 'XboxSpeechToTextOverlay', 'XboxIdentityProvider', 'XboxGameCallableUI',
    'MicrosoftStickyNotes', 'GetHelp', 'Getstarted', 'MicrosoftTips', 'Zune',
    'SkypeApp', 'people', 'WindowsMaps', 'WindowsAlarms', 'OfficeLens', '3DViewer',
    'Wallet', 'OneConnect', 'MixedReality', 'MSPaint', 'To-Do', 'PowerAutomate', 'ClipChamp')

function Get-WCInstalledApps {
    $apps = New-Object System.Collections.Generic.List[object]
    $seen = @{}
    $roots = @(@{ Path = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'; Scope = 'machine' },
               @{ Path = 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'; Scope = 'machine' })
    foreach ($h in Get-WCUserHives) { $roots += @{ Path = "$($h.Root)\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*"; Scope = 'user' } }
    foreach ($r in $roots) {
        $scope = $r.Scope
        Get-ItemProperty $r.Path -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -and $_.UninstallString } | ForEach-Object {
            if (-not $seen[$_.DisplayName]) {
                $seen[$_.DisplayName] = $true
                $apps.Add([pscustomobject]@{
                    Name = $_.DisplayName; Type = 'Win32'; Scope = $scope; Uninstall = $_.UninstallString; QuietUninstall = $_.QuietUninstallString
                    Version = "$($_.DisplayVersion)"; Publisher = "$($_.Publisher)"; PackageFullName = $null })
            }
        }
    }
    Get-AppxPackage -AllUsers -ErrorAction SilentlyContinue | Where-Object {
        $_.Name -notmatch '^(Microsoft\.(Windows\.|Desktop\.|NET\.|VCLibs\.|UI\.))'
    } | ForEach-Object {
        $n = "$($_.Name) [Store]"
        if (-not $seen[$n]) {
            $seen[$n] = $true
            $apps.Add([pscustomobject]@{
                Name = $n; Type = 'UWP'; Scope = 'machine'; Uninstall = $null; QuietUninstall = $null
                Version = "$($_.Version)"; Publisher = "$($_.Publisher)"; PackageFullName = $_.PackageFullName })
        }
    }
    return $apps
}

function ConvertTo-WCAppRow {
    param($App)
    return @{ name = $App.Name; type = $App.Type; scope = $App.Scope; version = $App.Version; publisher = $App.Publisher }
}

$WCTasks = [ordered]@{

    'list' = {
        Write-Log "Listando aplicativos instalados..."
        $apps = Get-WCInstalledApps | Sort-Object Name
        $rows = @($apps | ForEach-Object { ConvertTo-WCAppRow $_ })
        Write-Result @{ count = $rows.Count; apps = $rows }
        Write-Log "Total: $($rows.Count) apps encontrados" -Level SUCCESS
    }

    'list_bloatware' = {
        Write-Log "Procurando bloatware conhecido..."
        $hits = @(Get-WCInstalledApps | Where-Object { $n = $_.Name; $WCBloatware | Where-Object { $n -like "*$_*" } } | Sort-Object Name)
        $rows = @($hits | ForEach-Object { ConvertTo-WCAppRow $_ })
        foreach ($r in $rows) { Write-Log "  $($r.name)" }
        Write-Result @{ count = $rows.Count; apps = $rows }
        Write-Log "Bloatware encontrado: $($rows.Count) app(s)" -Level SUCCESS
    }

    'uninstall' = {
        $names = @("$(Get-Param 'apps' '')" -split '[,\r\n]' | ForEach-Object { $_.Trim() } | Where-Object { $_ } | Select-Object -Unique)
        if ($names.Count -eq 0) { throw 'Parâmetro apps vazio' }
        $dDir = [bool](Get-Param 'clean_folders' $true)
        $timeout = [int](Get-Param 'timeout_minutes' 15)
        if ($timeout -lt 1) { $timeout = 1 }

        $installed = Get-WCInstalledApps
        $t = $names.Count; $i = 0
        foreach ($an in $names) {
            $i++
            Update-Progress -Value ([Math]::Round(($i - 1) / $t * 100)) -Message "Desinstalando $an"
            Write-Log "[$i/$t] Desinstalando: $an"
            $info = $installed | Where-Object { $_.Name -eq $an -or $_.Name -eq "$an [Store]" } | Select-Object -First 1
            if (-not $info) { Write-Log "  Não encontrado entre os aplicativos instalados: $an" -Level WARN; continue }

            if ($info.Type -eq 'Win32' -and $info.Scope -eq 'user') {
                # O comando de desinstalação vem do Registro do próprio usuário: não é executado como SYSTEM.
                Write-Log "  Aplicativo instalado por usuário: desinstale na sessão do usuário (não executado como SYSTEM)" -Level WARN
            } elseif ($info.Type -eq 'Win32') {
                try {
                    $cmd6 = $info.Uninstall
                    if ($cmd6 -match 'msiexec') {
                        $guid6 = if ($cmd6 -match '({[0-9A-Fa-f-]{36}})') { $Matches[1] } else { $null }
                        if ($guid6) { $code = Start-WCProcess -FilePath 'msiexec.exe' -ArgumentList "/x $guid6 /quiet /norestart" -TimeoutMinutes $timeout }
                        else { $code = Start-WCProcess -FilePath 'cmd.exe' -ArgumentList "/c $cmd6 /quiet /norestart" -TimeoutMinutes $timeout }
                    } elseif ($info.QuietUninstall) {
                        $code = Start-WCProcess -FilePath 'cmd.exe' -ArgumentList "/c $($info.QuietUninstall)" -TimeoutMinutes $timeout
                    } else {
                        $code = Start-WCProcess -FilePath 'cmd.exe' -ArgumentList "/c $cmd6" -TimeoutMinutes $timeout
                    }
                    if ($code -in @(0, 1605, 1641, 3010)) { Write-Log "  Removido com sucesso (código $code)" -Level SUCCESS }
                    else { Write-Log "  Desinstalador terminou com código $code" -Level ERROR }
                } catch { Write-Log "  Erro: $_" -Level ERROR }
            } else {
                try {
                    if ($info.PackageFullName) { Remove-AppxPackage -Package $info.PackageFullName -AllUsers -ErrorAction Stop }
                    Write-Log "  Removido" -Level SUCCESS
                } catch { Write-Log "  Erro: $_" -Level ERROR }
            }

            if ($dDir) {
                $cn = ($an -replace ' \[Store\]', '' -replace '[^\w\.]', '')
                if ($cn.Length -ge 3) {
                    $dirs = @(@{ Path = (Join-Path $env:ProgramData $cn); Root = $env:ProgramData })
                    foreach ($p in Get-WCUserProfiles) {
                        $dirs += @{ Path = (Join-Path $p.AppData $cn); Root = $p.Path }
                        $dirs += @{ Path = (Join-Path $p.LocalAppData $cn); Root = $p.Path }
                    }
                    foreach ($d in $dirs) {
                        if (-not (Test-Path -LiteralPath $d.Path)) { continue }
                        if (-not (Test-WCReparseFree -Path (Split-Path $d.Path -Parent) -Root $d.Root)) { Write-Log "  Ignorado (junção no caminho): $($d.Path)" -Level WARN; continue }
                        try { [void](Remove-WCItemSafe -Path $d.Path); Write-Log "  Pasta residual: $($d.Path)" } catch { }
                    }
                }
            }
        }
        Write-Log "Desinstalação concluída: $t app(s)" -Level SUCCESS
    }
}

Invoke-WCTasks -Tasks $WCTasks
