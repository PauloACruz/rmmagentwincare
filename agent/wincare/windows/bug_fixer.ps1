<#
.SYNOPSIS
    Módulo bug_fixer: Correção de bugs do Windows (origem: WinCare Pro, módulo 03).
    Sem interface, roda como SYSTEM; ações de usuário percorrem os perfis em C:\Users.
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

# O Explorer encerrado volta pelo Winlogon (AutoRestartShell); se não voltar, é aberto na sessão do usuário.
$WCStartExplorer = 'if (-not (Get-Process explorer -ErrorAction SilentlyContinue | Where-Object { $_.SessionId -eq (Get-Process -Id $PID).SessionId })) { Start-Process explorer.exe }'

$WCTasks = [ordered]@{

    'wmi_repair' = {
        Write-Log "Reparando WMI..."
        Stop-Service winmgmt -Force -ErrorAction SilentlyContinue; Start-Sleep 1
        & winmgmt /resetrepository 2>&1 | ForEach-Object { Write-Log "  $_" }
        Start-Service winmgmt -ErrorAction SilentlyContinue
        Write-Log "WMI reparado" -Level SUCCESS
    }

    'rpc_check' = {
        Write-Log "Verificando RPC/DCOM..."
        foreach ($name in @('RpcSs', 'DcomLaunch', 'RpcEptMapper')) {
            $sv = Get-Service -Name $name -ErrorAction SilentlyContinue
            if ($sv -and $sv.Status -ne 'Running') { Start-Service $name -ErrorAction SilentlyContinue; Write-Log "  ${name}: reiniciado" -Level SUCCESS }
            else { Write-Log "  ${name}: OK" -Level SUCCESS }
        }
    }

    'event_log_clear' = {
        Write-Log "Limpando os logs de eventos Application e System..."
        foreach ($log in @('Application', 'System')) {
            try { Clear-EventLog -LogName $log -ErrorAction Stop; Write-Log "  ${log}: limpo" -Level SUCCESS }
            catch { Write-Log "  ${log}: aviso: $_" -Level WARN }
        }
    }

    'store_reset' = {
        Write-Log "Redefinindo a Microsoft Store..."
        Invoke-WCAsLoggedOnUser -Command 'Start-Process wsreset.exe -Wait' -TimeoutSeconds 300
        Write-Log "Store redefinida" -Level SUCCESS
    }

    'uwp_reregister' = {
        Write-Log "Registrando novamente os apps UWP..."
        $pkgs = @(Get-AppxPackage -AllUsers -ErrorAction SilentlyContinue)
        $i = 0
        foreach ($pkg in $pkgs) {
            $i++
            Add-AppxPackage -DisableDevelopmentMode -Register "$($pkg.InstallLocation)\AppXManifest.xml" -ErrorAction SilentlyContinue
            if ($i % 20 -eq 0) { Update-Progress -Value ([int]($i / $pkgs.Count * 100)) }
        }
        Write-Log "UWP registrados novamente ($($pkgs.Count) pacotes)" -Level SUCCESS
    }

    'dotnet_versions' = {
        Write-Log "Verificando o .NET Framework..."
        Get-ChildItem 'HKLM:\SOFTWARE\Microsoft\NET Framework Setup\NDP' -Recurse -ErrorAction SilentlyContinue |
            Where-Object { $_.GetValue('Version') -and $_.Name -match '\\v' } |
            ForEach-Object { Write-Log "  .NET $($_.GetValue('Version')) - $($_.Name.Split('\')[-1])" -Level SUCCESS }
    }

    'vcredist' = {
        Write-Log "Verificando os Visual C++ Redistributables..."
        Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
                         'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
            Where-Object { $_.DisplayName -match 'Visual C\+\+' } |
            Select-Object DisplayName, DisplayVersion |
            ForEach-Object { Write-Log "  $($_.DisplayName) - $($_.DisplayVersion)" -Level SUCCESS }
    }

    'gpupdate' = {
        Write-Log "Aplicando Políticas de Grupo..."
        & gpupdate /force 2>&1 | ForEach-Object { Write-Log "  $_" }
        Write-Log "GPO atualizado" -Level SUCCESS
    }

    'temp_permissions' = {
        Write-Log "Corrigindo permissões..."
        $targets = @(@{ Path = (Join-Path $env:SystemRoot 'Temp'); Root = $env:SystemRoot })
        foreach ($p in Get-WCUserProfiles) { $targets += @{ Path = $p.Temp; Root = $p.Path } }
        foreach ($t in $targets) {
            $p = $t.Path
            if (Test-Path -LiteralPath $p) {
                # icacls /t não pode alcançar pastas fora do perfil por meio de junções.
                $links = @(& cmd /c dir /s /b /al "$p" 2>$null | Where-Object { $_ })
                if (-not (Test-WCReparseFree -Path $p -Root $t.Root) -or $links.Count -gt 0) {
                    Write-Log "  Ignorado (contém junção ou link simbólico): $p" -Level WARN
                    continue
                }
                & icacls $p /reset /t /c /q 2>&1 | Out-Null
                if ($LASTEXITCODE -eq 0) { Write-Log "  OK: $p" -Level SUCCESS } else { Write-Log "  Aviso: $p (código $LASTEXITCODE)" -Level WARN }
            }
        }
    }

    'thumbnail_cache' = {
        Write-Log "Reconstruindo o cache de miniaturas..."
        Stop-Process -Name explorer -Force -ErrorAction SilentlyContinue; Start-Sleep 1
        foreach ($p in Get-WCUserProfiles) {
            $n = Clear-WCDirectory -Path (Join-Path $p.LocalAppData 'Microsoft\Windows\Explorer') -Root $p.Path -Filter 'thumbcache_*.db'
            Write-Log "  $($p.Name): $n arquivo(s) de miniaturas removido(s)"
        }
        Start-Sleep 4
        Invoke-WCAsLoggedOnUser -Command $WCStartExplorer -NoWait
        Write-Log "Cache de miniaturas limpo" -Level SUCCESS
    }

    'icon_cache' = {
        Write-Log "Limpando o cache de ícones..."
        foreach ($p in Get-WCUserProfiles) {
            $n = Clear-WCDirectory -Path $p.LocalAppData -Root $p.Path -Filter 'IconCache.db'
            $n += Clear-WCDirectory -Path (Join-Path $p.LocalAppData 'Microsoft\Windows\Explorer') -Root $p.Path -Filter 'iconcache_*.db'
            Write-Log "  $($p.Name): $n arquivo(s) de cache de ícones removido(s)"
        }
        Invoke-WCAsLoggedOnUser -Command 'ie4uinit.exe -ClearIconCache' -TimeoutSeconds 60
        Write-Log "Cache de ícones limpo; reinício recomendado" -Level SUCCESS
    }

    'font_cache' = {
        Write-Log "Limpando o cache de fontes..."
        Stop-Service -Name FontCache -Force -ErrorAction SilentlyContinue
        Remove-Item (Join-Path $env:SystemRoot 'ServiceProfiles\LocalService\AppData\Local\FontCache') -Recurse -Force -ErrorAction SilentlyContinue
        Start-Service -Name FontCache -ErrorAction SilentlyContinue
        Write-Log "Cache de fontes limpo" -Level SUCCESS
    }

    'dns_flush' = {
        Write-Log "Limpando DNS e NetBIOS..."
        & ipconfig /flushdns 2>&1 | ForEach-Object { Write-Log "  $_" }
        & nbtstat -RR 2>&1 | Out-Null
        Write-Log "DNS limpo" -Level SUCCESS
    }

    'search_restart' = {
        Write-Log "Reiniciando o Windows Search..."
        Stop-Service WSearch -Force -ErrorAction SilentlyContinue; Start-Sleep 2
        Start-Service WSearch -ErrorAction SilentlyContinue
        Write-Log "Windows Search reiniciado" -Level SUCCESS
    }

    'explorer_restart' = {
        Write-Log "Reiniciando o Windows Explorer..."
        Stop-Process -Name explorer -Force -ErrorAction SilentlyContinue; Start-Sleep 4
        Invoke-WCAsLoggedOnUser -Command $WCStartExplorer -NoWait
        Write-Log "Explorer reiniciado" -Level SUCCESS
    }
}

Invoke-WCTasks -Tasks $WCTasks
