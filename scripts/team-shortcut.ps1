param(
    [Parameter(Mandatory=$true)][string]$PythonExecutable,
    [Parameter(Mandatory=$true)][string]$Launcher,
    [Parameter(Mandatory=$true)][string]$Config,
    [Parameter(Mandatory=$true)][string]$AppExecutable
)
$ErrorActionPreference = 'Stop'
foreach ($item in @($PythonExecutable, $Launcher, $Config, $AppExecutable)) {
    if (-not (Test-Path -LiteralPath $item -PathType Leaf)) { throw "Launcher file missing: $item" }
    if ($item.Contains('"')) { throw 'Launcher paths cannot contain a quote' }
}
$desktop = [Environment]::GetFolderPath('Desktop')
$shortcutPath = Join-Path $desktop 'Agent Teams.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
if ((Test-Path -LiteralPath $shortcutPath) -and $shortcut.Arguments -notlike '*team-desktop.py*') {
    throw "A different shortcut already exists at $shortcutPath"
}
$shortcut.TargetPath = $PythonExecutable
$shortcut.Arguments = '"' + $Launcher + '" --config "' + $Config + '"'
$shortcut.WorkingDirectory = Split-Path -Parent $Launcher
$shortcut.Description = 'Project planners, missions, workers, and agent org charts'
$shortcut.IconLocation = $AppExecutable + ',0'
$shortcut.Save()
Write-Output "Created $shortcutPath"
