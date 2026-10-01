<#
.SYNOPSIS
    Módulo component_test: Testes de hardware e componentes (origem: WinCare Pro, módulo 08).
    Sem interface, roda como SYSTEM. Relatórios que o original abria na tela são salvos em
    %ProgramData%\WinCare\reports e informados em um evento result.
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

$WCTasks = [ordered]@{

    'cpu_info' = {
        $cpu8 = Get-CimInstance Win32_Processor -ErrorAction SilentlyContinue | Select-Object -First 1
        Write-Log "--- PROCESSADOR ---"
        Write-Log "  Modelo : $($cpu8.Name)"
        Write-Log "  Núcleos: $($cpu8.NumberOfCores) físicos / $($cpu8.NumberOfLogicalProcessors) lógicos"
        Write-Log "  Clock  : $($cpu8.MaxClockSpeed) MHz"
        Write-Log "  Cache  : L2=$($cpu8.L2CacheSize)KB  L3=$($cpu8.L3CacheSize)KB"
        Write-Log "  Carga  : $($cpu8.LoadPercentage)%"
        Write-Result @{ model = "$($cpu8.Name)"; cores = $cpu8.NumberOfCores; threads = $cpu8.NumberOfLogicalProcessors; maxClockMHz = $cpu8.MaxClockSpeed; loadPercent = $cpu8.LoadPercentage }
    }

    'cpu_bench' = {
        Write-Log "--- BENCHMARK DE CPU ---"
        Write-Log "  Calculando 100.000 números primos..."
        $sw8 = [System.Diagnostics.Stopwatch]::StartNew()
        $pr8 = 0; $n8 = 2
        while ($pr8 -lt 100000) {
            $ip = $true
            for ($d = 2; $d -le [Math]::Sqrt($n8); $d++) { if ($n8 % $d -eq 0) { $ip = $false; break } }
            if ($ip) { $pr8++ }; $n8++
            if ($pr8 % 10000 -eq 0 -and $ip) { Update-Progress -Value ([int]($pr8 / 1000)) }
        }
        $sw8.Stop()
        $secs = $sw8.Elapsed.TotalSeconds
        $perf8 = if ($secs -lt 1.5) { 'Excelente' } elseif ($secs -lt 4) { 'Bom' } else { 'Regular' }
        Write-Log ("  100 mil primos em {0:F3}s: {1}" -f $secs, $perf8) -Level SUCCESS
        Write-Result @{ test = 'primes'; primes = 100000; seconds = [Math]::Round($secs, 3); rating = $perf8 }
    }

    'cpu_stress' = {
        Write-Log "--- TESTE DE ESTRESSE DA CPU (5 s) ---"
        $cStart = (Get-CimInstance Win32_Processor -ErrorAction SilentlyContinue | Measure-Object LoadPercentage -Average).Average
        $nc = (Get-CimInstance Win32_ComputerSystem -ErrorAction SilentlyContinue).NumberOfLogicalProcessors
        if (-not $nc) { $nc = [Environment]::ProcessorCount }
        $jobs8 = @()
        for ($j = 0; $j -lt $nc; $j++) { $jobs8 += Start-Job { while ($true) { } } }
        Start-Sleep 5
        $cEnd = (Get-CimInstance Win32_Processor -ErrorAction SilentlyContinue | Measure-Object LoadPercentage -Average).Average
        $jobs8 | Stop-Job -PassThru | Remove-Job -Force
        Write-Log "  Antes: $cStart% | Depois de 5 s de estresse: $cEnd%"
        Write-Log "  $(if ($cEnd -gt 70) { 'CPU respondeu ao estresse: OK' } else { 'CPU pode ter limitado o desempenho (throttling)' })"
        Write-Result @{ test = 'stress'; loadBefore = $cStart; loadAfter = $cEnd }
    }

    'ram_info' = {
        Write-Log "--- MEMÓRIA RAM ---"
        $stks = Get-CimInstance Win32_PhysicalMemory -ErrorAction SilentlyContinue
        $totalRAM = ($stks | Measure-Object -Property Capacity -Sum).Sum / 1GB
        Write-Log "  Total: $([Math]::Round($totalRAM, 1)) GB"
        foreach ($st8 in $stks) {
            Write-Log "  $($st8.DeviceLocator): $([Math]::Round($st8.Capacity / 1GB, 0)) GB @ $($st8.Speed) MHz - $($st8.Manufacturer) [$($st8.MemoryType)]"
        }
        $os8 = Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue
        $free8 = [Math]::Round($os8.FreePhysicalMemory / 1MB, 1)
        Write-Log "  Disponível agora: $free8 GB"
        Write-Result @{ totalGB = [Math]::Round($totalRAM, 1); availableGB = $free8; modules = @($stks).Count }
    }

    'ram_usage' = {
        Write-Log "--- 10 PROCESSOS COM MAIOR USO DE MEMÓRIA ---"
        $top = @()
        Get-Process -ErrorAction SilentlyContinue | Sort-Object WorkingSet64 -Descending | Select-Object -First 10 | ForEach-Object {
            $mb = [Math]::Round($_.WorkingSet64 / 1MB, 1)
            Write-Log "  $($_.ProcessName.PadRight(28)) $mb MB"
            $top += @{ name = $_.ProcessName; pid = $_.Id; workingSetMB = $mb }
        }
        Write-Result @{ processes = @($top) }
    }

    # mdsched.exe é interativo; o equivalente sem interface é agendar o {memdiag} no próximo boot.
    'ram_test' = {
        Write-Log "--- DIAGNÓSTICO DE MEMÓRIA ---"
        & bcdedit /bootsequence '{memdiag}' 2>&1 | ForEach-Object { Write-Log "  $_" }
        if ($LASTEXITCODE -eq 0) { Write-Log "  Diagnóstico de memória agendado para a próxima reinicialização" -Level SUCCESS }
        else { Write-Log "  Não foi possível agendar o diagnóstico (código $LASTEXITCODE)" -Level ERROR }
    }

    'disk_info' = {
        Write-Log "--- DISCOS FÍSICOS ---"
        $disks = @()
        Get-CimInstance Win32_DiskDrive -ErrorAction SilentlyContinue | ForEach-Object {
            Write-Log "  $($_.Model) | $([Math]::Round($_.Size / 1GB, 0)) GB | Partições: $($_.Partitions) | Interface: $($_.InterfaceType)"
            $disks += @{ model = "$($_.Model)"; sizeGB = [Math]::Round($_.Size / 1GB, 0); interface = "$($_.InterfaceType)" }
        }
        Write-Log "--- VOLUMES LÓGICOS ---"
        $vols = @()
        Get-CimInstance Win32_LogicalDisk -ErrorAction SilentlyContinue | Where-Object { $_.Size } | ForEach-Object {
            $fp = [Math]::Round($_.FreeSpace / 1GB, 1); $tot = [Math]::Round($_.Size / 1GB, 0)
            $pct = [Math]::Round($_.FreeSpace / $_.Size * 100)
            Write-Log "  $($_.DeviceID) [$($_.VolumeName)]  $fp GB livres / $tot GB ($pct%)"
            $vols += @{ volume = $_.DeviceID; freeGB = $fp; totalGB = $tot; freePercent = $pct }
        }
        Write-Result @{ disks = @($disks); volumes = @($vols) }
    }

    'disk_speed' = {
        Write-Log "--- VELOCIDADE DO DISCO ---"
        $tf8 = Join-Path $Global:WC.TempDir "wc_dt_$(Get-Random).tmp"
        $buf8 = New-Object byte[] (1MB); (New-Object System.Random).NextBytes($buf8)
        try {
            $sw2 = [System.Diagnostics.Stopwatch]::StartNew()
            $fs8 = [System.IO.File]::Open($tf8, 'CreateNew', 'Write', 'None')
            1..100 | ForEach-Object { $fs8.Write($buf8, 0, $buf8.Length) }
            $fs8.Flush($true); $fs8.Close(); $sw2.Stop()
            $wMBs = [Math]::Round(100 / $sw2.Elapsed.TotalSeconds, 1)
            Write-Log "  Escrita sequencial : $wMBs MB/s" -Level SUCCESS
            Update-Progress -Value 50
            $sw2.Restart()
            $fs8b = [System.IO.File]::OpenRead($tf8)
            $rb8 = New-Object byte[] (1MB)
            while ($fs8b.Read($rb8, 0, $rb8.Length) -gt 0) { }
            $fs8b.Close(); $sw2.Stop()
            $rMBs = [Math]::Round(100 / $sw2.Elapsed.TotalSeconds, 1)
            Write-Log "  Leitura sequencial : $rMBs MB/s" -Level SUCCESS
            $tipo8 = if ($rMBs -gt 400) { 'SSD NVMe' } elseif ($rMBs -gt 150) { 'SSD SATA' } elseif ($rMBs -gt 80) { 'HDD rápido' } else { 'HDD lento' }
            Write-Log "  Tipo estimado: $tipo8"
            Write-Result @{ sizeMB = 100; writeMBps = $wMBs; readMBps = $rMBs; estimatedType = $tipo8 }
        } catch {
            Write-Log "  Erro no teste: $_" -Level ERROR
        } finally {
            Remove-Item -LiteralPath $tf8 -Force -ErrorAction SilentlyContinue
        }
    }

    'disk_smart' = {
        Write-Log "--- STATUS S.M.A.R.T. ---"
        $rows = @()
        Get-CimInstance -Namespace root\WMI -ClassName MSStorageDriver_FailurePredictStatus -ErrorAction SilentlyContinue | ForEach-Object {
            $status8 = if ($_.PredictFailure) { 'FALHA PREVISTA' } else { 'OK' }
            Write-Log "  Disco: $($_.InstanceName): $status8" -Level $(if ($_.PredictFailure) { 'ERROR' } else { 'SUCCESS' })
            $rows += @{ disk = $_.InstanceName; predictFailure = [bool]$_.PredictFailure }
        }
        if ($rows.Count -eq 0) { Write-Log "  Nenhum disco informou status S.M.A.R.T. via WMI" }
        Write-Result @{ disks = @($rows) }
    }

    'gpu_info' = {
        Write-Log "--- GPU ---"
        Get-CimInstance Win32_VideoController -ErrorAction SilentlyContinue | ForEach-Object {
            Write-Log "  $($_.Name)"
            Write-Log "  VRAM  : $([Math]::Round($_.AdapterRAM / 1MB, 0)) MB"
            Write-Log "  Resolução: $($_.CurrentHorizontalResolution)x$($_.CurrentVerticalResolution) @ $($_.CurrentRefreshRate) Hz"
            Write-Log "  Driver : $($_.DriverVersion)  ($($_.DriverDate))"
        }
    }

    'dxdiag' = {
        Write-Log "--- RELATÓRIO DIRECTX ---"
        $dxOut = Join-Path (Get-WCDataDir 'reports') "WinCare_dxdiag_$(Get-Date -Format 'yyyyMMdd_HHmmss').txt"
        $null = Start-WCProcess -FilePath 'dxdiag.exe' -ArgumentList "/t `"$dxOut`"" -TimeoutMinutes 5
        if (Test-Path -LiteralPath $dxOut) {
            Write-Log "  Relatório DX: $dxOut" -Level SUCCESS
            Write-Result @{ report = $dxOut }
        } else { Write-Log "  Relatório DX não foi gerado" -Level WARN }
    }

    'net_ping' = {
        Write-Log "--- CONECTIVIDADE ---"
        $rows = @()
        foreach ($target in @('8.8.8.8', '1.1.1.1', 'google.com', 'cloudflare.com')) {
            $r8 = Test-Connection $target -Count 4 -ErrorAction SilentlyContinue
            if ($r8) {
                $avg8 = [Math]::Round(($r8 | Measure-Object ResponseTime -Average).Average, 0)
                $min8 = ($r8 | Measure-Object ResponseTime -Minimum).Minimum
                $max8 = ($r8 | Measure-Object ResponseTime -Maximum).Maximum
                $q8 = if ($avg8 -lt 20) { 'Excelente' } elseif ($avg8 -lt 50) { 'Bom' } elseif ($avg8 -lt 100) { 'Regular' } else { 'Ruim' }
                Write-Log "  ${target}: média ${avg8}ms  mín ${min8}ms  máx ${max8}ms ($q8)" -Level SUCCESS
                $rows += @{ target = $target; ok = $true; avgMs = $avg8; minMs = $min8; maxMs = $max8; rating = $q8 }
            } else {
                Write-Log "  ${target}: SEM RESPOSTA" -Level WARN
                $rows += @{ target = $target; ok = $false }
            }
        }
        $dns = @()
        foreach ($name in @('google.com', 'cloudflare.com')) {
            try {
                $ips = @([System.Net.Dns]::GetHostAddresses($name) | ForEach-Object { $_.IPAddressToString })
                Write-Log "  DNS ${name}: $($ips -join ', ')" -Level SUCCESS
                $dns += @{ name = $name; ok = $true; addresses = $ips }
            } catch {
                Write-Log "  DNS ${name}: falhou ($($_.Exception.Message))" -Level WARN
                $dns += @{ name = $name; ok = $false }
            }
        }
        Write-Result @{ ping = @($rows); dns = @($dns) }
    }

    'net_trace' = {
        Write-Log "--- TRACEROUTE (8.8.8.8) ---"
        & tracert -h 10 8.8.8.8 2>&1 | Where-Object { $_ -match '\S' } | ForEach-Object { Write-Log "  $_" }
    }

    'net_adapters' = {
        Write-Log "--- ADAPTADORES DE REDE ---"
        $rows = @()
        Get-NetAdapter -ErrorAction SilentlyContinue | ForEach-Object {
            $ip8 = (Get-NetIPAddress -InterfaceAlias $_.Name -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1).IPAddress
            Write-Log "  $("$($_.Name)".PadRight(30)) $("$($_.Status)".PadRight(12)) $("$($_.LinkSpeed)".PadRight(12)) IP: $ip8"
            $rows += @{ name = "$($_.Name)"; status = "$($_.Status)"; linkSpeed = "$($_.LinkSpeed)"; ipv4 = "$ip8" }
        }
        Write-Result @{ adapters = @($rows) }
    }

    # No original esta opção existia na tela sem implementação; aqui mede o download de 25 MB.
    'net_speed' = {
        Write-Log "--- VELOCIDADE DE INTERNET (Cloudflare) ---"
        $bytes = 25000000
        $tmp = Join-Path $Global:WC.TempDir "wc_speed_$(Get-Random).bin"
        try {
            $sw = [System.Diagnostics.Stopwatch]::StartNew()
            Invoke-WebRequest -Uri "https://speed.cloudflare.com/__down?bytes=$bytes" -OutFile $tmp -UseBasicParsing -TimeoutSec 120 -ErrorAction Stop
            $sw.Stop()
            $size = (Get-Item -LiteralPath $tmp).Length
            $mbps = [Math]::Round(($size * 8 / 1e6) / $sw.Elapsed.TotalSeconds, 1)
            Write-Log "  Download: $mbps Mbit/s ($([Math]::Round($size / 1MB, 1)) MB em $([Math]::Round($sw.Elapsed.TotalSeconds, 1)) s)" -Level SUCCESS
            Write-Result @{ downloadMbps = $mbps; bytes = $size; seconds = [Math]::Round($sw.Elapsed.TotalSeconds, 2) }
        } catch {
            Write-Log "  Falha no teste de velocidade: $_" -Level WARN
        } finally {
            Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        }
    }

    'battery' = {
        Write-Log "--- BATERIA ---"
        if (-not (Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue)) { Skip-Task 'Bateria não detectada (desktop)' }
        $br8 = Join-Path (Get-WCDataDir 'reports') "WinCare_battery_$(Get-Date -Format 'yyyyMMdd_HHmmss').html"
        & powercfg /batteryreport /output $br8 2>&1 | ForEach-Object { Write-Log "  $_" }
        if (Test-Path -LiteralPath $br8) { Write-Log "  Relatório: $br8" -Level SUCCESS; Write-Result @{ report = $br8 } }
        else { Write-Log "  Relatório de bateria não foi gerado" -Level WARN }
    }

    'power_efficiency' = {
        Write-Log "--- EFICIÊNCIA ENERGÉTICA ---"
        $er8 = Join-Path (Get-WCDataDir 'reports') "WinCare_energy_$(Get-Date -Format 'yyyyMMdd_HHmmss').html"
        Write-Log "  Coletando dados por 10 segundos..."
        & powercfg /energy /output $er8 /duration 10 2>&1 | ForEach-Object { Write-Log "  $_" }
        if (Test-Path -LiteralPath $er8) { Write-Log "  Relatório: $er8" -Level SUCCESS; Write-Result @{ report = $er8 } }
        else { Write-Log "  Relatório de energia não foi gerado" -Level WARN }
    }
}

Invoke-WCTasks -Tasks $WCTasks
