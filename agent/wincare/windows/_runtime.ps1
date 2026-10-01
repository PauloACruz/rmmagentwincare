<#
.SYNOPSIS
    Harness comum dos módulos WinCare executados pelo agente.

    Roda como SYSTEM, sem interface e sem interação. Cada módulo faz dot-source
    deste arquivo, define suas tarefas em um dicionário ordenado
    (chave -> scriptblock) e chama Invoke-WCTasks.

    Protocolo: cada evento é uma linha "##WC {json}" no stdout com os tipos
    log, progress, task e result. O agente acrescenta seq e time e publica.
    Caracteres fora do ASCII são escapados como \uXXXX para não depender da
    página de código do console.

    Entrada: variável de ambiente WINCARE_REQUEST com o caminho de um JSON
    { run_id, module, tasks: [], params: {} } gravado pelo agente.
#>

Set-StrictMode -Off
$ErrorActionPreference = 'Continue'
$ProgressPreference    = 'SilentlyContinue'
$ConfirmPreference     = 'None'

try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch { }
try { $OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch { }
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

$wcProgramData = if ($env:ProgramData) { $env:ProgramData } else { [IO.Path]::GetTempPath() }
$Global:WC = @{
    Version      = '2.12.0'
    DataDir      = Join-Path $wcProgramData 'WinCare'
    TempDir      = if ($env:WINCARE_DIR -and (Test-Path -LiteralPath $env:WINCARE_DIR)) { $env:WINCARE_DIR } else { $env:TEMP }
    Selected     = @()
    TaskCount    = 0
    TaskIndex    = 0
    CurrentTask  = $null
    TaskHadWarn  = $false
    TaskHadError = $false
    SkipReason   = $null
}
$Global:Params = @{}

# -- Emissão de eventos ----------------------------------------------------------

function ConvertTo-WCJson {
    param($InputObject)
    $json = ConvertTo-Json -InputObject $InputObject -Compress -Depth 8
    return [regex]::Replace($json, '[^\x00-\x7E]', { param($m) '\u{0:x4}' -f [int][char]$m.Value })
}

function Send-WCEvent {
    param([hashtable]$WCEvent)
    $line = '##WC ' + (ConvertTo-WCJson $WCEvent)
    [Console]::Out.WriteLine($line)
    [Console]::Out.Flush()
}

function Write-Log {
    param(
        [Parameter(Position = 0)]$Message,
        [ValidateSet('INFO', 'WARN', 'ERROR', 'SUCCESS', 'DEBUG')]
        [string]$Level = 'INFO',
        [switch]$NoFile
    )
    $text = ("$Message" -replace "`0", '').TrimEnd()
    if ($text.Trim() -eq '') { return }
    if ($Level -eq 'DEBUG') { $Level = 'INFO' }
    if ($text.Length -gt 4000) { $text = $text.Substring(0, 4000) + '...' }
    if ($Global:WC.CurrentTask) {
        if ($Level -eq 'ERROR') { $Global:WC.TaskHadError = $true }
        elseif ($Level -eq 'WARN') { $Global:WC.TaskHadWarn = $true }
    }
    Send-WCEvent @{ type = 'log'; level = $Level; message = $text }
}

# Value é o percentual da tarefa atual; o harness converte para o progresso geral.
function Update-Progress {
    param([int]$Value, [string]$Message)
    $v = [Math]::Max(0, [Math]::Min(100, $Value))
    if ($Global:WC.TaskCount -gt 0 -and $Global:WC.CurrentTask) {
        $v = [int][Math]::Floor((($Global:WC.TaskIndex - 1) + ($v / 100)) / $Global:WC.TaskCount * 100)
    }
    $ev = @{ type = 'progress'; value = $v }
    if ($Message) { $ev.message = $Message }
    Send-WCEvent $ev
}

function Write-Result {
    param([Parameter(Mandatory = $true)][hashtable]$Data)
    if ($Global:WC.CurrentTask -and -not $Data.ContainsKey('task')) { $Data.task = $Global:WC.CurrentTask }
    Send-WCEvent @{ type = 'result'; data = $Data }
}

# Encerra a tarefa atual com status skipped (não se aplica a esta máquina).
function Skip-Task {
    param([string]$Reason)
    $Global:WC.SkipReason = $Reason
    throw "WC_SKIP: $Reason"
}

function Get-Param {
    param([string]$Name, $Default = $null)
    if ($Global:Params.ContainsKey($Name) -and $null -ne $Global:Params[$Name]) { return $Global:Params[$Name] }
    return $Default
}

# -- Utilitários para execução como SYSTEM ---------------------------------------

# Pasta de dados persistente (backups e relatórios), acessível só a SYSTEM e Administradores.
function Get-WCDataDir {
    param([string]$Sub)
    $root = $Global:WC.DataDir
    if (-not (Test-Path -LiteralPath $root)) {
        New-Item -ItemType Directory -Path $root -Force | Out-Null
        & icacls $root /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' 2>&1 | Out-Null
    }
    if (-not $Sub) { return $root }
    $p = Join-Path $root $Sub
    if (-not (Test-Path -LiteralPath $p)) { New-Item -ItemType Directory -Path $p -Force | Out-Null }
    return $p
}

# Perfis de usuário em C:\Users (substitui %USERPROFILE%, %APPDATA%, %LOCALAPPDATA% e %TEMP%).
function Get-WCUserProfiles {
    $usersRoot = if ($env:PUBLIC) { Split-Path $env:PUBLIC -Parent } else { Join-Path $env:SystemDrive 'Users' }
    $skip = @('Public', 'Default', 'Default User', 'All Users', 'defaultuser0', 'WDAGUtilityAccount')
    Get-ChildItem -LiteralPath $usersRoot -Directory -Force -ErrorAction SilentlyContinue |
        Where-Object { $skip -notcontains $_.Name -and -not ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -and (Test-Path -LiteralPath (Join-Path $_.FullName 'NTUSER.DAT')) } |
        ForEach-Object {
            [pscustomobject]@{
                Name         = $_.Name
                Path         = $_.FullName
                AppData      = Join-Path $_.FullName 'AppData\Roaming'
                LocalAppData = Join-Path $_.FullName 'AppData\Local'
                Temp         = Join-Path $_.FullName 'AppData\Local\Temp'
            }
        }
}

# Hives de usuários carregados (equivalente ao HKCU de cada usuário com sessão ou perfil carregado).
function Get-WCUserHives {
    Get-ChildItem -Path 'Registry::HKEY_USERS' -ErrorAction SilentlyContinue |
        Where-Object { $_.PSChildName -match '^S-1-(5-21|12-1)-[\d-]+$' } |
        ForEach-Object {
            [pscustomobject]@{
                SID     = $_.PSChildName
                Root    = "Registry::HKEY_USERS\$($_.PSChildName)"
                Classes = "Registry::HKEY_USERS\$($_.PSChildName)_Classes"
            }
        }
}

# Pastas de usuário e C:\Windows\Temp podem receber junções criadas por usuários comuns.
# Como SYSTEM, nunca seguimos pontos de nova análise: o link é removido, o destino não.
function Test-WCReparseFree {
    param([Parameter(Mandatory = $true)][string]$Path, [string]$Root)
    $full = [IO.Path]::GetFullPath($Path)
    $stop = if ($Root) { [IO.Path]::GetFullPath($Root).TrimEnd('\') } else { [IO.Path]::GetPathRoot($full).TrimEnd('\') }
    $cur = $full.TrimEnd('\')
    while ($cur -and $cur.Length -gt $stop.Length) {
        if (Test-Path -LiteralPath $cur) {
            $attr = [IO.File]::GetAttributes($cur)
            if ($attr -band [IO.FileAttributes]::ReparsePoint) { return $false }
        }
        $cur = [IO.Path]::GetDirectoryName($cur)
    }
    return $true
}

function Remove-WCItemSafe {
    param([Parameter(Mandatory = $true)][string]$Path)
    $attr = [IO.File]::GetAttributes($Path)
    $isDir = [bool]($attr -band [IO.FileAttributes]::Directory)
    if ($attr -band [IO.FileAttributes]::ReparsePoint) {
        if ($isDir) { [IO.Directory]::Delete($Path, $false) } else { [IO.File]::Delete($Path) }
        return 1
    }
    if (-not $isDir) {
        if ($attr -band [IO.FileAttributes]::ReadOnly) { [IO.File]::SetAttributes($Path, $attr -band (-bnot [IO.FileAttributes]::ReadOnly)) }
        [IO.File]::Delete($Path)
        return 1
    }
    $n = 0
    foreach ($child in [IO.Directory]::GetFileSystemEntries($Path)) {
        try { $n += Remove-WCItemSafe -Path $child } catch { }
    }
    try { [IO.Directory]::Delete($Path, $false); $n++ } catch { }
    return $n
}

# Apaga o conteúdo de uma pasta sem seguir junções. Devolve a quantidade de itens removidos.
function Clear-WCDirectory {
    param([Parameter(Mandatory = $true)][string]$Path, [string]$Root, [string]$Filter = '*')
    if (-not (Test-Path -LiteralPath $Path)) { return 0 }
    if (-not (Test-WCReparseFree -Path $Path -Root $Root)) {
        Write-Log "  Ignorado (contém junção ou link simbólico no caminho): $Path" -Level WARN
        return 0
    }
    $n = 0
    foreach ($child in [IO.Directory]::GetFileSystemEntries($Path, $Filter)) {
        try { $n += Remove-WCItemSafe -Path $child } catch { }
    }
    return $n
}

function Get-WCLoggedOnUsers {
    $users = @()
    Get-CimInstance Win32_Process -Filter "Name='explorer.exe'" -ErrorAction SilentlyContinue | ForEach-Object {
        $o = Invoke-CimMethod -InputObject $_ -MethodName GetOwner -ErrorAction SilentlyContinue
        if ($o -and $o.ReturnValue -eq 0 -and $o.User) { $users += "$($o.Domain)\$($o.User)" }
    }
    return @($users | Select-Object -Unique)
}

# Executa um trecho PowerShell fixo (definido no script, nunca texto do usuário) na sessão de
# cada usuário conectado, via tarefa agendada temporária. Usado para ações que no original
# dependiam do usuário logado (wsreset, OneDrive, Explorer, cmdkey).
function Invoke-WCAsLoggedOnUser {
    param(
        [Parameter(Mandatory = $true)][string]$Command,
        [int]$TimeoutSeconds = 600,
        [switch]$NoWait
    )
    $users = Get-WCLoggedOnUsers
    if ($users.Count -eq 0) {
        Write-Log "Nenhum usuário conectado: a ação na sessão do usuário não foi executada." -Level WARN
        return
    }
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Command))
    $psExe = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    foreach ($u in $users) {
        $name = 'WinCare-' + [guid]::NewGuid().ToString('N')
        try {
            $action    = New-ScheduledTaskAction -Execute $psExe -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -EncodedCommand $encoded"
            $principal = New-ScheduledTaskPrincipal -UserId $u -LogonType Interactive -RunLevel Limited
            $settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Seconds ([Math]::Max($TimeoutSeconds, 60)))
            Register-ScheduledTask -TaskName $name -Action $action -Principal $principal -Settings $settings -Force -ErrorAction Stop | Out-Null
            Start-ScheduledTask -TaskName $name -ErrorAction Stop
            Write-Log "  Executado na sessão de $u"
            Start-Sleep -Seconds 2
            if (-not $NoWait) {
                $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
                while ((Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue).State -eq 'Running' -and (Get-Date) -lt $deadline) {
                    Start-Sleep -Seconds 2
                }
            }
        } catch {
            Write-Log "  Falha ao executar na sessão de ${u}: $_" -Level WARN
        } finally {
            Unregister-ScheduledTask -TaskName $name -Confirm:$false -ErrorAction SilentlyContinue
        }
    }
}

# Start-Process com tempo limite: processos que abririam interface na sessão 0 não travam a execução.
function Start-WCProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string]$ArgumentList,
        [int]$TimeoutMinutes = 60
    )
    $sp = @{ FilePath = $FilePath; PassThru = $true; WindowStyle = 'Hidden'; ErrorAction = 'Stop' }
    if ($ArgumentList) { $sp.ArgumentList = $ArgumentList }
    $proc = Start-Process @sp
    if (-not $proc.WaitForExit($TimeoutMinutes * 60 * 1000)) {
        Write-Log "  $FilePath excedeu $TimeoutMinutes min e foi encerrado" -Level WARN
        & taskkill /T /F /PID $proc.Id 2>&1 | Out-Null
        return -1
    }
    return $proc.ExitCode
}

# winget não fica no PATH do SYSTEM: procura o executável do App Installer.
function Get-WCWinget {
    $cmd = Get-Command winget.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $dirs = Get-ChildItem -Path (Join-Path $env:ProgramFiles 'WindowsApps') -Filter 'Microsoft.DesktopAppInstaller_*__8wekyb3d8bbwe' -Directory -ErrorAction SilentlyContinue |
        Sort-Object { try { [version](($_.Name -split '_')[1]) } catch { [version]'0.0' } } -Descending
    foreach ($d in $dirs) {
        $exe = Join-Path $d.FullName 'winget.exe'
        if (Test-Path -LiteralPath $exe) { return $exe }
    }
    return $null
}

# Linhas úteis da saída do winget (remove indicadores de progresso).
function Select-WCWingetLines {
    param([Parameter(ValueFromPipeline = $true)]$Line)
    process {
        $s = ("$Line" -replace "`0", '')
        if ($s -match '\r') { $s = ($s -split '\r')[-1] }
        $s = $s.TrimEnd()
        if ($s -match '\S' -and $s -notmatch '^\s*[-\\|/]\s*$' -and $s -notmatch '[\u2580-\u259F]') { $s }
    }
}

# Converte a tabela do winget (cabeçalho + linha de traços) em objetos.
function ConvertFrom-WCWingetTable {
    param([string[]]$Lines)
    $sep = -1
    for ($i = 0; $i -lt $Lines.Count; $i++) { if ($Lines[$i] -match '^-{10,}\s*$') { $sep = $i; break } }
    if ($sep -lt 1) { return @() }
    $header = $Lines[$sep - 1]
    $starts = @([regex]::Matches($header, '\S+') | ForEach-Object { $_.Index })
    if ($starts.Count -lt 3) { return @() }
    $rows = @()
    for ($i = $sep + 1; $i -lt $Lines.Count; $i++) {
        $l = $Lines[$i]
        if ($l.Length -le $starts[1] -or $l -match '^\d+ .*(upgrade|atualiza)') { continue }
        $cols = @()
        for ($c = 0; $c -lt $starts.Count; $c++) {
            $from = $starts[$c]
            if ($from -ge $l.Length) { $cols += ''; continue }
            $to = if ($c + 1 -lt $starts.Count) { [Math]::Min($starts[$c + 1], $l.Length) } else { $l.Length }
            $cols += $l.Substring($from, $to - $from).Trim()
        }
        $row = @{ name = $cols[0]; id = $cols[1]; version = $cols[2] }
        if ($cols.Count -ge 5) { $row.available = $cols[3] }
        if ($cols.Count -ge 4) { $row.source = $cols[$cols.Count - 1] }
        if ($row.id) { $rows += $row }
    }
    return $rows
}

# -- Execução das tarefas --------------------------------------------------------

function Read-WCRequest {
    $path = $env:WINCARE_REQUEST
    if (-not $path -or -not (Test-Path -LiteralPath $path)) { throw 'WINCARE_REQUEST ausente' }
    $req = Get-Content -LiteralPath $path -Raw -Encoding UTF8 | ConvertFrom-Json
    $Global:WC.Selected = @($req.tasks | Where-Object { $_ })
    if ($req.params) {
        foreach ($p in $req.params.PSObject.Properties) { $Global:Params[$p.Name] = $p.Value }
    }
}

function Invoke-WCTasks {
    param(
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Tasks,
        [scriptblock]$Prelude
    )
    try {
        Read-WCRequest
    } catch {
        Write-Log "Falha ao ler a requisição: $_" -Level ERROR
        exit 2
    }
    $sel = $Global:WC.Selected
    $Global:WC.TaskCount = $sel.Count
    Update-Progress -Value 0

    if ($Prelude) {
        try {
            & $Prelude 2>&1 | ForEach-Object { Write-Log "$_" }
        } catch {
            Write-Log "Falha na preparação: $_" -Level WARN
        }
    }

    $i = 0
    foreach ($key in $sel) {
        $i++
        $Global:WC.TaskIndex = $i
        if (-not $Tasks.Contains($key)) {
            Send-WCEvent @{ type = 'task'; key = $key; status = 'error'; message = 'Tarefa não implementada neste sistema' }
            continue
        }
        $Global:WC.CurrentTask  = $key
        $Global:WC.TaskHadWarn  = $false
        $Global:WC.TaskHadError = $false
        $Global:WC.SkipReason   = $null
        Send-WCEvent @{ type = 'task'; key = $key; status = 'running' }
        Update-Progress -Value 0

        $status = 'ok'
        $msg = $null
        try {
            & $Tasks[$key] 2>&1 | ForEach-Object {
                if ($_ -is [System.Management.Automation.ErrorRecord]) { Write-Log "$_" -Level WARN } else { Write-Log "$_" }
            }
            if ($Global:WC.TaskHadError) { $status = 'error' }
            elseif ($Global:WC.TaskHadWarn) { $status = 'warning' }
        } catch {
            if ($Global:WC.SkipReason) {
                $status = 'skipped'
                $msg = $Global:WC.SkipReason
                Write-Log "Tarefa ignorada: $msg"
            } else {
                $status = 'error'
                $msg = "$($_.Exception.Message)"
                Write-Log "Erro em ${key}: $msg" -Level ERROR
            }
        }
        $Global:WC.CurrentTask = $null
        $ev = @{ type = 'task'; key = $key; status = $status }
        if ($msg) { $ev.message = $msg }
        Send-WCEvent $ev
        Update-Progress -Value ([int][Math]::Floor($i / [Math]::Max($sel.Count, 1) * 100))
    }
    if ($Global:WC.TaskIndex -ne 0) { Update-Progress -Value 100 }
}
