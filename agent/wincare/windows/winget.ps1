<#
.SYNOPSIS
    Módulo winget: Gerenciador de aplicativos (origem: WinCare Pro, módulo 09).
    Sem interface, roda como SYSTEM. Busca e listas viram eventos result; os IDs e
    termos de busca chegam pelo arquivo de parâmetros e são passados como argumentos
    separados (nunca concatenados em linha de comando).
#>
. (Join-Path $PSScriptRoot '_runtime.ps1')

function Get-WCWingetOrSkip {
    $wg = Get-WCWinget
    if (-not $wg) { Skip-Task 'winget não detectado. Instale o App Installer pela Microsoft Store ou atualize o Windows.' }
    return $wg
}

function Split-WCIds {
    param([string]$Value)
    return @($Value -split '[,;\s]+' | ForEach-Object { $_.Trim() } | Where-Object { $_ } | Select-Object -Unique)
}

$WCTasks = [ordered]@{

    'search' = {
        $wg = Get-WCWingetOrSkip
        $q9 = "$(Get-Param 'query' '')".Trim()
        if (-not $q9) { throw 'Parâmetro query vazio' }
        if ($q9.StartsWith('-')) { throw 'Termo de busca inválido' }
        Write-Log "Buscando no winget: $q9"
        $lines = @(& $wg search --query $q9 --accept-source-agreements --disable-interactivity 2>&1 | Select-WCWingetLines)
        $rows = @(ConvertFrom-WCWingetTable -Lines $lines)
        foreach ($r in $rows) { Write-Log "  $($r.name) | $($r.id) | $($r.version)" }
        Write-Result @{ query = $q9; count = $rows.Count; packages = $rows }
        Write-Log "$($rows.Count) resultado(s) encontrado(s)" -Level SUCCESS
    }

    'install' = {
        $wg = Get-WCWingetOrSkip
        $ids = Split-WCIds "$(Get-Param 'ids' '')"
        if ($ids.Count -eq 0) { throw 'Parâmetro ids vazio' }
        $bad = @($ids | Where-Object { $_ -notmatch '^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$' })
        if ($bad.Count -gt 0) { throw "ID de pacote inválido: $($bad -join ', ')" }
        $silent = [bool](Get-Param 'silent' $true)
        Write-Log "== INSTALAÇÃO EM LOTE: $($ids.Count) app(s) =="
        $i9 = 0; $tot9 = $ids.Count
        foreach ($id9c in $ids) {
            $i9++
            Update-Progress -Value ([Math]::Round(($i9 - 1) / $tot9 * 100)) -Message "Instalando $id9c"
            Write-Log "[$i9/$tot9] Instalando: $id9c"
            $wgArgs = @('install', '--id', $id9c, '--exact', '--accept-source-agreements', '--accept-package-agreements', '--disable-interactivity')
            if ($silent) { $wgArgs += '--silent' }
            & $wg @wgArgs 2>&1 | Select-WCWingetLines | ForEach-Object { Write-Log "  $_" }
            if ($LASTEXITCODE -eq 0) { Write-Log "  $id9c concluído" -Level SUCCESS }
            else { Write-Log "  $id9c falhou (código $LASTEXITCODE)" -Level ERROR }
        }
        Write-Log "== INSTALAÇÃO EM LOTE: CONCLUÍDA ==" -Level SUCCESS
    }

    'list_upgrades' = {
        $wg = Get-WCWingetOrSkip
        Write-Log "Verificando atualizações disponíveis via winget..."
        $lines = @(& $wg upgrade --accept-source-agreements --disable-interactivity 2>&1 | Select-WCWingetLines)
        $lines | ForEach-Object { Write-Log "  $_" }
        $rows = @(ConvertFrom-WCWingetTable -Lines $lines)
        Write-Result @{ count = $rows.Count; packages = $rows }
    }

    'upgrade_all' = {
        $wg = Get-WCWingetOrSkip
        Write-Log "Atualizando todos os apps via winget..."
        Update-Progress -Value 5
        $wgParams = @('upgrade', '--all', '--accept-source-agreements', '--accept-package-agreements', '--disable-interactivity')
        if ([bool](Get-Param 'include_unknown' $true)) { $wgParams += '--include-unknown' }
        & $wg @wgParams 2>&1 | Select-WCWingetLines | ForEach-Object { Write-Log "  $_" }
        if ($LASTEXITCODE -eq 0) { Write-Log "Atualização de todos os apps concluída" -Level SUCCESS }
        else { Write-Log "winget terminou com código $LASTEXITCODE" -Level WARN }
    }

    'export' = {
        $wg = Get-WCWingetOrSkip
        $exportPath = Join-Path (Get-WCDataDir 'reports') "winget_apps_$(Get-Date -Format 'yyyyMMdd_HHmmss').json"
        Write-Log "Exportando a lista de apps para: $exportPath"
        & $wg export -o $exportPath --accept-source-agreements --disable-interactivity 2>&1 | Select-WCWingetLines | ForEach-Object { Write-Log "  $_" }
        if (-not (Test-Path -LiteralPath $exportPath)) { throw 'winget export não gerou o arquivo' }
        $content = Get-Content -LiteralPath $exportPath -Raw -Encoding UTF8 | ConvertFrom-Json
        Write-Result @{ path = $exportPath; export = $content }
        Write-Log "Lista exportada: $exportPath" -Level SUCCESS
    }

    'import' = {
        $wg = Get-WCWingetOrSkip
        $importPath = "$(Get-Param 'path' '')".Trim()
        if ($importPath -notmatch '^[A-Za-z]:\\' -or -not (Test-Path -LiteralPath $importPath -PathType Leaf)) { throw "Arquivo não encontrado: $importPath" }
        Write-Log "Importando e instalando apps de: $importPath"
        & $wg import -i $importPath --accept-source-agreements --accept-package-agreements --disable-interactivity 2>&1 | Select-WCWingetLines | ForEach-Object { Write-Log "  $_" }
        if ($LASTEXITCODE -eq 0) { Write-Log "Importação concluída" -Level SUCCESS }
        else { Write-Log "winget import terminou com código $LASTEXITCODE" -Level WARN }
    }
}

Invoke-WCTasks -Tasks $WCTasks
