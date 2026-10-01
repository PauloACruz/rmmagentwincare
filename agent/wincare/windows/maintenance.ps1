<#
.SYNOPSIS
    Módulo maintenance: Manutenção corretiva e preventiva do Windows
    (origem: WinCare Pro, módulo 01). Sem interface, roda como SYSTEM.
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

$WCTasks = [ordered]@{

    # -- Integridade do sistema --------------------------------------------------
    'sfc' = {
        Write-Log "Executando SFC /scannow (pode demorar alguns minutos)..."
        Update-Progress -Value 5 -Message "Iniciando SFC..."
        & sfc /scannow 2>&1 | ForEach-Object { Write-Log $_ }
        if ($LASTEXITCODE -eq 0) {
            Write-Log "SFC concluído com sucesso" -Level SUCCESS
        } else {
            Write-Log "SFC reportou problemas (verifique o log do Windows)" -Level WARN
        }
    }

    'dism_check' = {
        Write-Log "DISM /Online /Cleanup-Image /CheckHealth..."
        & DISM /Online /Cleanup-Image /CheckHealth 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "DISM CheckHealth concluído" -Level SUCCESS
    }

    'dism_restore' = {
        Write-Log "DISM /Online /Cleanup-Image /RestoreHealth (requer internet)..."
        Update-Progress -Value 5 -Message "Iniciando RestoreHealth (pode demorar)..."
        & DISM /Online /Cleanup-Image /RestoreHealth 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "DISM RestoreHealth concluído" -Level SUCCESS
    }

    # -- Rede ---------------------------------------------------------------------
    'winsock_reset' = {
        Write-Log "Redefinindo Winsock..."
        & netsh winsock reset 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "Redefinindo TCP/IP..."
        & netsh int ip reset 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "Limpando cache DNS..."
        & ipconfig /flushdns 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "Rede redefinida com sucesso. REINICIALIZAÇÃO NECESSÁRIA." -Level SUCCESS
    }

    'net_adapter_reset' = {
        Write-Log "Reiniciando adaptadores de rede..."
        Get-NetAdapter | ForEach-Object {
            Write-Log "  Reiniciando: $($_.Name)"
            Disable-NetAdapter -Name $_.Name -Confirm:$false -ErrorAction SilentlyContinue
            Start-Sleep -Milliseconds 500
            Enable-NetAdapter -Name $_.Name -Confirm:$false -ErrorAction SilentlyContinue
        }
        Write-Log "Adaptadores reiniciados" -Level SUCCESS
    }

    # -- Limpeza ------------------------------------------------------------------
    'temp_files' = {
        Write-Log "Limpando arquivos temporários..."
        $targets = @()
        foreach ($p in Get-WCUserProfiles) { $targets += @{ Path = $p.Temp; Root = $p.Path } }
        $targets += @{ Path = (Join-Path $env:SystemRoot 'Temp'); Root = $env:SystemRoot }
        $targets += @{ Path = (Join-Path $env:SystemRoot 'Prefetch'); Root = $env:SystemRoot }
        $totalRemoved = 0
        $n = 0
        foreach ($t in $targets) {
            $n++
            if (Test-Path -LiteralPath $t.Path) {
                $count = Clear-WCDirectory -Path $t.Path -Root $t.Root
                Write-Log "  $($t.Path) : $count itens removidos" -Level SUCCESS
                $totalRemoved += $count
            }
            Update-Progress -Value ([int]($n / $targets.Count * 100))
        }
        Write-Log "Total removido: $totalRemoved itens temporários" -Level SUCCESS
    }

    # Como SYSTEM, Clear-RecycleBin só alcança a lixeira do próprio SYSTEM; por isso a
    # limpeza é feita direto em <unidade>\$Recycle.Bin\<SID> de todos os usuários.
    'recycle_bin' = {
        Write-Log "Esvaziando a Lixeira..."
        $removed = 0
        $drives = Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' -ErrorAction SilentlyContinue
        foreach ($d in $drives) {
            $bin = Join-Path ($d.DeviceID + '\') '$Recycle.Bin'
            if (-not (Test-Path -LiteralPath $bin)) { continue }
            Get-ChildItem -LiteralPath $bin -Directory -Force -ErrorAction SilentlyContinue |
                Where-Object { -not ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) } | ForEach-Object {
                    foreach ($f in @('$R*', '$I*')) { $removed += Clear-WCDirectory -Path $_.FullName -Root $bin -Filter $f }
                }
        }
        Write-Log "Lixeira esvaziada ($removed itens)" -Level SUCCESS
    }

    'windows_logs' = {
        Write-Log "Analisando logs de eventos antigos..."
        $cutoff = (Get-Date).AddDays(-30)
        foreach ($log in @('Application', 'System', 'Security', 'Setup')) {
            try {
                $events = Get-EventLog -LogName $log -Before $cutoff -ErrorAction SilentlyContinue
                if ($events) {
                    Write-Log "  ${log}: $($events.Count) eventos antigos encontrados"
                }
            } catch { }
        }
        Write-Log "Análise de logs concluída" -Level SUCCESS
    }

    'soft_distrib' = {
        Write-Log "Parando o serviço Windows Update..."
        Stop-Service -Name wuauserv -Force -ErrorAction SilentlyContinue
        Stop-Service -Name bits -Force -ErrorAction SilentlyContinue
        $sdPath = Join-Path $env:SystemRoot 'SoftwareDistribution\Download'
        if (Test-Path -LiteralPath $sdPath) {
            $count = @(Get-ChildItem -LiteralPath $sdPath -Recurse -Force -ErrorAction SilentlyContinue).Count
            Remove-Item -Path "$sdPath\*" -Recurse -Force -ErrorAction SilentlyContinue
            Write-Log "SoftwareDistribution limpa: $count itens removidos" -Level SUCCESS
        }
        Start-Service -Name wuauserv -ErrorAction SilentlyContinue
        Start-Service -Name bits -ErrorAction SilentlyContinue
        Write-Log "Serviços do Windows Update reiniciados" -Level SUCCESS
    }

    # -- Disco --------------------------------------------------------------------
    'disk_cleanup' = {
        Write-Log "Executando a Limpeza de Disco..."
        $regPath = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\VolumeCaches'
        Get-ChildItem $regPath | ForEach-Object {
            Set-ItemProperty $_.PSPath -Name 'StateFlags0064' -Value 2 -Type DWord -ErrorAction SilentlyContinue
        }
        $code = Start-WCProcess -FilePath 'cleanmgr.exe' -ArgumentList '/sagerun:64' -TimeoutMinutes 60
        Write-Log "Limpeza de disco concluída (código $code)" -Level SUCCESS
    }

    'defrag' = {
        Write-Log "Verificando o tipo do disco C:..."
        $disk = Get-PhysicalDisk | Where-Object { $_.DeviceId -eq 0 }
        if ($disk -and $disk.MediaType -eq 'SSD') {
            Write-Log "SSD detectado: executando TRIM em vez de desfragmentação"
            & defrag C: /L /U 2>&1 | ForEach-Object { Write-Log $_ }
        } else {
            Write-Log "HDD detectado: iniciando desfragmentação (pode demorar)..."
            & defrag C: /U /V 2>&1 | ForEach-Object { Write-Log $_ }
        }
        Write-Log "Otimização de disco concluída" -Level SUCCESS
    }

    'chkdsk_schedule' = {
        Write-Log "Agendando CHKDSK para a próxima reinicialização..."
        & chkntfs /x C: 2>&1 | Out-Null
        Write-Output "Y" | chkdsk C: /f /r /x 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "CHKDSK agendado. Ele será executado na próxima reinicialização." -Level SUCCESS
    }

    # -- Inicialização ------------------------------------------------------------
    'bcd_repair' = {
        Write-Log "ATENÇÃO: reparo do BCD, operação crítica!" -Level WARN
        Write-Log "Fazendo backup do BCD atual..."
        $bcdbak = Join-Path (Get-WCDataDir 'backups') "bcd_backup_$(Get-Date -Format 'yyyyMMdd_HHmmss').bak"
        & bcdedit /export $bcdbak 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "Backup salvo em: $bcdbak" -Level SUCCESS
        if (-not (Get-Command bootrec.exe -ErrorAction SilentlyContinue)) {
            Write-Log "bootrec.exe não encontrado (normalmente só existe no WinRE). Reparo não executado." -Level ERROR
            return
        }
        Write-Log "Reconstruindo o BCD..."
        & bootrec /fixmbr 2>&1 | ForEach-Object { Write-Log $_ }
        & bootrec /fixboot 2>&1 | ForEach-Object { Write-Log $_ }
        & bootrec /scanos 2>&1 | ForEach-Object { Write-Log $_ }
        & bootrec /rebuildbcd 2>&1 | ForEach-Object { Write-Log $_ }
        Write-Log "Reparo do BCD concluído" -Level SUCCESS
    }

    # -- Serviços -----------------------------------------------------------------
    'critical_services' = {
        Write-Log "Verificando serviços críticos do Windows..."
        $criticalServices = @(
            @{ Name = 'wuauserv';         Display = 'Windows Update' },
            @{ Name = 'bits';             Display = 'Background Transfer' },
            @{ Name = 'cryptsvc';         Display = 'Cryptographic Services' },
            @{ Name = 'msiserver';        Display = 'Windows Installer' },
            @{ Name = 'trustedinstaller'; Display = 'Trusted Installer' },
            @{ Name = 'winmgmt';          Display = 'WMI' },
            @{ Name = 'eventlog';         Display = 'Event Log' },
            @{ Name = 'spooler';          Display = 'Print Spooler' }
        )
        foreach ($svc in $criticalServices) {
            $s = Get-Service -Name $svc.Name -ErrorAction SilentlyContinue
            if ($null -eq $s) {
                Write-Log "  $($svc.Display): NÃO ENCONTRADO" -Level WARN
            } elseif ($s.Status -ne 'Running') {
                Write-Log "  $($svc.Display): parado, tentando iniciar..."
                try {
                    Start-Service -Name $svc.Name -ErrorAction Stop
                    Write-Log "  $($svc.Display): iniciado com sucesso" -Level SUCCESS
                } catch {
                    Write-Log "  $($svc.Display): falha ao iniciar: $_" -Level ERROR
                }
            } else {
                Write-Log "  $($svc.Display): OK (em execução)" -Level SUCCESS
            }
        }
    }
}

# Ponto de restauração antes das operações de risco (como no original).
$WCPrelude = {
    $risky = @('bcd_repair', 'chkdsk_schedule', 'dism_restore', 'winsock_reset')
    if ($Global:WC.Selected | Where-Object { $risky -contains $_ }) {
        Write-Log "Criando ponto de restauração do sistema..."
        try {
            Enable-ComputerRestore -Drive "$env:SystemDrive\" -ErrorAction SilentlyContinue
            Checkpoint-Computer -Description "WinCare - Pré-manutenção $(Get-Date -Format 'yyyy-MM-dd HH:mm')" `
                -RestorePointType 'MODIFY_SETTINGS' -ErrorAction Stop
            Write-Log "Ponto de restauração criado com sucesso" -Level SUCCESS
        } catch {
            Write-Log "Aviso: não foi possível criar o ponto de restauração: $_" -Level WARN
        }
    }
}

Invoke-WCTasks -Tasks $WCTasks -Prelude $WCPrelude
