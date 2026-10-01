<#
.SYNOPSIS
    Módulo windows_update: Windows Update recursivo (origem: WinCare Pro, módulo 02).
    Sem interface, roda como SYSTEM.
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

function Get-WCPendingUpdates {
    param([bool]$IncludeDrivers = $false)
    $s = New-Object -ComObject Microsoft.Update.Session
    $q = if ($IncludeDrivers) { 'IsInstalled=0' } else { "IsInstalled=0 and Type='Software'" }
    return $s.CreateUpdateSearcher().Search($q).Updates
}

$WCTasks = [ordered]@{

    'check_pending' = {
        $ups = Get-WCPendingUpdates
        $c = $ups.Count
        Write-Log "Pendentes: $c" -Level $(if ($c -eq 0) { 'SUCCESS' } else { 'INFO' })
        $list = @()
        foreach ($u in $ups) {
            Write-Log "  * $($u.Title)"
            $list += @{ title = $u.Title; kb = (@($u.KBArticleIDs) -join ','); sizeMB = [Math]::Round($u.MaxDownloadSize / 1MB, 1) }
        }
        Write-Result @{ count = $c; updates = @($list) }
        if ($c -eq 0) { Write-Log "Sistema 100% atualizado" -Level SUCCESS }
    }

    'history' = {
        $sess = New-Object -ComObject Microsoft.Update.Session
        $sr = $sess.CreateUpdateSearcher()
        $n = $sr.GetTotalHistoryCount()
        $items = @()
        if ($n -gt 0) {
            foreach ($item in $sr.QueryHistory(0, [Math]::Min($n, 15))) {
                $ok = $item.ResultCode -eq 2
                Write-Log ("  {0}  {1}  {2}" -f $(if ($ok) { 'OK  ' } else { 'FALHA' }), $item.Date.ToString('dd/MM/yy'), $item.Title)
                $items += @{ date = $item.Date.ToString('o'); title = $item.Title; resultCode = [int]$item.ResultCode; success = $ok }
            }
        } else {
            Write-Log "Nenhum histórico encontrado."
        }
        Write-Result @{ count = $items.Count; history = @($items) }
    }

    'defender_signatures' = {
        Write-Log "Atualizando o Defender..."
        try {
            Update-MpSignature -ErrorAction Stop
            Write-Log "Defender atualizado" -Level SUCCESS
        } catch {
            Write-Log "Aviso Defender: $_" -Level WARN
        }
    }

    'store_apps' = {
        $wg = Get-WCWinget
        if (-not $wg) { Skip-Task 'winget não encontrado nesta máquina' }
        Write-Log "Atualizando apps da Microsoft Store..."
        & $wg upgrade --source msstore --all --accept-source-agreements --accept-package-agreements --silent 2>&1 |
            Select-WCWingetLines | ForEach-Object { Write-Log "  $_" }
        if ($LASTEXITCODE -ne 0) {
            Write-Log "winget terminou com código $LASTEXITCODE (a origem msstore pode não estar disponível para SYSTEM)" -Level WARN
        } else {
            Write-Log "Store atualizada" -Level SUCCESS
        }
    }

    'install_updates' = {
        $doLoop    = [bool](Get-Param 'recursive' $true)
        $doDrivers = [bool](Get-Param 'drivers' $false)
        $doReboot  = [bool](Get-Param 'auto_reboot' $false)
        Write-Log "== WINDOWS UPDATE: INÍCIO =="
        Update-Progress -Value 2

        if (-not (Get-Module -ListAvailable -Name PSWindowsUpdate -ErrorAction SilentlyContinue)) {
            Write-Log "Instalando PSWindowsUpdate..."
            try {
                Install-PackageProvider -Name NuGet -MinimumVersion 2.8.5.201 -Force -Scope AllUsers -ErrorAction Stop | Out-Null
                Install-Module PSWindowsUpdate -Force -Scope AllUsers -ErrorAction Stop
                Write-Log "PSWindowsUpdate instalado" -Level SUCCESS
            } catch {
                Write-Log "PSWindowsUpdate indisponível, usando a API COM: $_"
            }
        }

        $rodada = 0; $total = 0; $max = if ($doLoop) { 5 } else { 1 }
        do {
            $rodada++
            Write-Log "--- RODADA $rodada/$max ---"
            $pendentes = 0
            try {
                $psOk = Get-Module -ListAvailable -Name PSWindowsUpdate -ErrorAction SilentlyContinue
                if ($psOk) {
                    Import-Module PSWindowsUpdate -Force -ErrorAction SilentlyContinue
                    $wuArgs = @{ MicrosoftUpdate = $true; AcceptAll = $true; IgnoreReboot = $true; ErrorAction = 'SilentlyContinue' }
                    if (-not $doDrivers) { $wuArgs.NotCategory = 'Drivers' }
                    $ups = @(Get-WindowsUpdate @wuArgs)
                    $pendentes = $ups.Count
                    Write-Log "  Encontradas: $pendentes"
                    if ($pendentes -gt 0) {
                        $ups | ForEach-Object { Write-Log "  Baixando: $($_.Title)" }
                        Install-WindowsUpdate @wuArgs 2>&1 | Out-String -Stream | Where-Object { $_ -match '\S' } | ForEach-Object { Write-Log "  $_" }
                        $total += $pendentes
                    }
                } else {
                    Write-Log "  Usando a API nativa do Windows Update..."
                    $sess2 = New-Object -ComObject Microsoft.Update.Session
                    $res2 = Get-WCPendingUpdates -IncludeDrivers $doDrivers
                    $pendentes = $res2.Count
                    Write-Log "  Encontradas: $pendentes"
                    if ($pendentes -gt 0) {
                        $col = New-Object -ComObject Microsoft.Update.UpdateColl
                        foreach ($u2 in $res2) {
                            Write-Log "  Baixando: $($u2.Title)"
                            if (-not $u2.EulaAccepted) { $u2.AcceptEula() }
                            $col.Add($u2) | Out-Null
                        }
                        $dlr = $sess2.CreateUpdateDownloader(); $dlr.Updates = $col
                        Write-Log "  Baixando $pendentes atualização(ões)..."
                        $dlr.Download() | Out-Null
                        $inst = $sess2.CreateUpdateInstaller(); $inst.Updates = $col
                        Write-Log "  Instalando..."
                        $ir = $inst.Install()
                        Write-Log "  Resultado: $($ir.ResultCode) | Reinício: $($ir.RebootRequired)"
                        $total += $pendentes
                    }
                }
            } catch {
                Write-Log "  Erro na rodada ${rodada}: $_" -Level ERROR
                break
            }
            Update-Progress -Value ([Math]::Min(15 + ($rodada * 16), 95)) -Message "Rodada $rodada concluída"
            if ($pendentes -eq 0) { Write-Log "Nenhuma atualização pendente!" -Level SUCCESS; break }
        } while ($rodada -lt $max -and $doLoop)

        Write-Log "== WINDOWS UPDATE: CONCLUÍDO ==" -Level SUCCESS
        Write-Log " Total instalado: $total | Rodadas: $rodada" -Level SUCCESS

        $needReboot = $false
        try { $needReboot = (New-Object -ComObject Microsoft.Update.SystemInfo).RebootRequired } catch { }
        Write-Result @{ installed = $total; rounds = $rodada; rebootRequired = [bool]$needReboot }
        if ($doReboot -and $needReboot) {
            # Agenda em vez de reiniciar na hora para o agente publicar o fim da execução.
            Write-Log "Reiniciando em 60 segundos..." -Level WARN
            & shutdown.exe /r /t 60 /c "WinCare: reinicialização após atualizações" 2>&1 | ForEach-Object { Write-Log $_ }
        } elseif ($needReboot) {
            Write-Log "Reinicialização pendente para concluir as atualizações." -Level WARN
        }
    }
}

Invoke-WCTasks -Tasks $WCTasks
