<#
.SYNOPSIS
    Módulo registry: Correção e limpeza do Registro (origem: WinCare Pro, módulo 05).
    Sem interface, roda como SYSTEM. O que era HKCU é aplicado a cada hive de usuário
    carregado em HKEY_USERS. Backup antes de qualquer alteração, como no original.
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

$Global:WCRegBackupDone = $null

function Invoke-WCRegistryBackup {
    if ($Global:WCRegBackupDone) {
        Write-Log "Backup já realizado nesta execução: $Global:WCRegBackupDone" -Level SUCCESS
        return
    }
    Write-Log "Criando backup do Registro..."
    $bk = Join-Path (Get-WCDataDir 'backups') "Registry_$(Get-Date -Format 'yyyyMMdd_HHmmss')"
    & reg export HKLM "${bk}_HKLM.reg" /y 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Write-Log "Backup: ${bk}_HKLM.reg" -Level SUCCESS } else { Write-Log "Falha no backup do HKLM (código $LASTEXITCODE)" -Level WARN }
    foreach ($h in Get-WCUserHives) {
        & reg export "HKU\$($h.SID)" "${bk}_HKU_$($h.SID).reg" /y 2>&1 | Out-Null
        if ($LASTEXITCODE -eq 0) { Write-Log "Backup: ${bk}_HKU_$($h.SID).reg" -Level SUCCESS } else { Write-Log "Falha no backup de HKU\$($h.SID)" -Level WARN }
    }
    $Global:WCRegBackupDone = "${bk}_*.reg"
}

function Get-WCRunKeyPaths {
    param([string]$Leaf)
    $paths = @("HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\$Leaf")
    foreach ($h in Get-WCUserHives) { $paths += "$($h.Root)\SOFTWARE\Microsoft\Windows\CurrentVersion\$Leaf" }
    return $paths
}

$WCTasks = [ordered]@{

    'backup' = { Invoke-WCRegistryBackup }

    'startup_invalid' = {
        Write-Log "Verificando entradas de inicialização inválidas..."
        $removed = 0
        foreach ($rk in Get-WCRunKeyPaths 'Run') {
            if (-not (Test-Path $rk)) { continue }
            $props = Get-ItemProperty $rk -ErrorAction SilentlyContinue
            $props.PSObject.Properties | Where-Object { $_.Name -notmatch '^PS' } | ForEach-Object {
                $exe = $_.Value -replace '"', '' -split ' ' | Select-Object -First 1
                $exe = [Environment]::ExpandEnvironmentVariables("$exe")
                if ($exe -and $exe -match '\.' -and -not (Test-Path $exe) -and $exe -notmatch 'rundll32|regsvr32') {
                    Write-Log "  Inválido: $($_.Name) -> $exe"
                    Remove-ItemProperty -Path $rk -Name $_.Name -Force -ErrorAction SilentlyContinue
                    Write-Log "  Removido: $($_.Name)" -Level SUCCESS; $removed++
                }
            }
        }
        Write-Log "Inicialização: $removed entrada(s) inválida(s) removida(s)" -Level SUCCESS
    }

    'runonce_clear' = {
        Write-Log "Limpando RunOnce residual..."
        foreach ($rk in Get-WCRunKeyPaths 'RunOnce') {
            if (-not (Test-Path $rk)) { continue }
            $item = Get-Item $rk -ErrorAction SilentlyContinue
            $propNames = @($item.Property)
            if ($propNames.Count -gt 0) {
                foreach ($pn in $propNames) {
                    Remove-ItemProperty -Path $rk -Name $pn -Force -ErrorAction SilentlyContinue
                    Write-Log "  RunOnce removido: $pn" -Level SUCCESS
                }
                Write-Log "  $($propNames.Count) entradas RunOnce removidas de $rk" -Level SUCCESS
            }
        }
    }

    'missing_services' = {
        Write-Log "Verificando drivers e serviços ausentes..."
        $orphaned = 0
        Get-ChildItem 'HKLM:\SYSTEM\CurrentControlSet\Services' -ErrorAction SilentlyContinue | ForEach-Object {
            $sp = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
            if ($sp.ImagePath -and $sp.ImagePath -match '\.sys$|\.exe$') {
                $img = $sp.ImagePath -replace '"', '' -replace '\\SystemRoot', $env:SystemRoot -split ' ' | Select-Object -First 1
                $img = [Environment]::ExpandEnvironmentVariables("$img")
                if ($img -match '^(?i)system32\\') { $img = Join-Path $env:SystemRoot $img }
                if ($img -and -not (Test-Path $img)) {
                    Write-Log "  Ausente: $($_.PSChildName) -> $img"
                    $orphaned++
                }
            }
        }
        Write-Log "Drivers/serviços ausentes: $orphaned (apenas relatados, não removidos)"
    }

    'orphan_software' = {
        Write-Log "Verificando software órfão..."
        $orf = 0
        $roots = @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall',
                   'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall')
        foreach ($h in Get-WCUserHives) { $roots += "$($h.Root)\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall" }
        foreach ($root in $roots) {
            if (-not (Test-Path $root)) { continue }
            Get-ChildItem $root -ErrorAction SilentlyContinue | ForEach-Object {
                $p = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
                if ($p -and $p.UninstallString) {
                    $exe = $p.UninstallString -replace '"', '' -split ' ' | Select-Object -First 1
                    if ($exe -and $exe -match '\.' -and -not (Test-Path $exe) -and $exe -notmatch 'msiexec|MsiExec') {
                        Write-Log "  Órfão: $($p.DisplayName) -> $exe"; $orf++
                    }
                }
            }
        }
        Write-Log "Software órfão detectado: $orf entrada(s); verifique manualmente"
    }

    'muicache' = {
        Write-Log "Limpando MUICache..."
        foreach ($h in Get-WCUserHives) {
            $mc = "$($h.Classes)\Local Settings\Software\Microsoft\Windows\Shell\MuiCache"
            if (Test-Path $mc) { Remove-Item $mc -Recurse -Force -ErrorAction SilentlyContinue; Write-Log "  MUICache limpo: $($h.SID)" -Level SUCCESS }
        }
    }

    'appcompat' = {
        Write-Log "Limpando AppCompatFlags..."
        foreach ($h in Get-WCUserHives) {
            $ac = "$($h.Root)\Software\Microsoft\Windows NT\CurrentVersion\AppCompatFlags\Compatibility Assistant\Store"
            if (-not (Test-Path $ac)) { continue }
            $props = Get-ItemProperty $ac -ErrorAction SilentlyContinue
            $props.PSObject.Properties | Where-Object { $_.Name -notmatch '^PS' -and -not (Test-Path -LiteralPath $_.Name -ErrorAction SilentlyContinue) } | ForEach-Object {
                Remove-ItemProperty -Path $ac -Name $_.Name -Force -ErrorAction SilentlyContinue
                Write-Log "  Removido: $($_.Name)" -Level SUCCESS
            }
        }
    }

    'file_associations' = {
        Write-Log "Verificando associações de arquivo..."
        foreach ($ext in @('.txt', '.html', '.pdf', '.docx', '.xlsx', '.jpg', '.mp4')) {
            $r = & cmd /c "assoc $ext" 2>&1
            Write-Log "  $r"
        }
    }

    'shell_extensions' = {
        Write-Log "Verificando extensões de shell..."
        $shPath = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Shell Extensions\Approved'
        if (Test-Path $shPath) {
            $props = Get-ItemProperty $shPath -ErrorAction SilentlyContinue
            $props.PSObject.Properties | Where-Object { $_.Name -notmatch '^PS' -and $_.Name -match '^\{' } | ForEach-Object {
                Write-Log "  Extensão de shell: $($_.Name) = $($_.Value)"
            }
            Write-Log "Extensões de shell listadas (remoção manual recomendada)"
        }
    }

    'open_with' = {
        Write-Log "Verificando listas Abrir com..."
        foreach ($h in Get-WCUserHives) {
            $owPath = "$($h.Root)\Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts"
            if (Test-Path $owPath) {
                Write-Log "  Usuário $($h.SID):"
                Get-ChildItem $owPath -ErrorAction SilentlyContinue | Select-Object -First 20 | ForEach-Object { Write-Log "    $($_.PSChildName)" }
            }
        }
    }

    'prefetch' = {
        Write-Log "Otimizando Prefetch e Superfetch..."
        $pk = 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters'
        if (Test-Path $pk) {
            Set-ItemProperty $pk -Name EnablePrefetcher -Value 3 -Type DWord -ErrorAction SilentlyContinue
            Set-ItemProperty $pk -Name EnableSuperfetch -Value 3 -Type DWord -ErrorAction SilentlyContinue
            Write-Log "Prefetch/Superfetch configurados (valor 3 = otimizado)" -Level SUCCESS
        }
    }

    'start_menu_search' = {
        Write-Log "Verificando o cache de pesquisa do Menu Iniciar..."
        foreach ($h in Get-WCUserHives) {
            if (Test-Path "$($h.Root)\Software\Microsoft\Windows\CurrentVersion\Search") {
                Write-Log "  Cache de pesquisa verificado: $($h.SID)" -Level SUCCESS
            }
        }
    }

    'recent_docs' = {
        Write-Log "Limpando documentos recentes..."
        foreach ($h in Get-WCUserHives) {
            $rd = "$($h.Root)\Software\Microsoft\Windows\CurrentVersion\Explorer\RecentDocs"
            if (Test-Path $rd) {
                $item = Get-Item $rd -ErrorAction SilentlyContinue
                if ($item.Property) { $item.Property | ForEach-Object { Remove-ItemProperty -Path $rd -Name $_ -Force -ErrorAction SilentlyContinue } }
                Write-Log "  Documentos recentes limpos: $($h.SID)" -Level SUCCESS
            }
        }
    }

    'run_mru' = {
        Write-Log "Limpando o histórico do Executar..."
        foreach ($h in Get-WCUserHives) {
            $rm = "$($h.Root)\Software\Microsoft\Windows\CurrentVersion\Explorer\RunMRU"
            if (Test-Path $rm) {
                $item = Get-Item $rm -ErrorAction SilentlyContinue
                if ($item.Property) { $item.Property | ForEach-Object { Remove-ItemProperty -Path $rm -Name $_ -Force -ErrorAction SilentlyContinue } }
                Write-Log "  Histórico do Executar limpo: $($h.SID)" -Level SUCCESS
            }
        }
    }
}

# Backup sempre antes de tudo, como no original (inclusive quando 'backup' não foi marcado).
$WCPrelude = {
    $users = @(Get-WCUserHives).Count
    Write-Log "Hives de usuário carregados: $users (perfis sem sessão não são alterados)"
    Invoke-WCRegistryBackup
}

Invoke-WCTasks -Tasks $WCTasks -Prelude $WCPrelude
