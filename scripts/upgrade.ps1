[CmdletBinding()]
param(
    [string]$Version,
    [switch]$BuildFromSource
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest
$ProjectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
Set-Location $ProjectRoot

if (-not (Test-Path -LiteralPath ".env")) {
    throw "No .env file was found. Run scripts\install.ps1 for a new installation."
}
if ($Version -and $Version -notmatch "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$") {
    throw "The panel image version contains unsupported characters."
}

$BackupDirectory = Join-Path $ProjectRoot "data\upgrades"
New-Item -ItemType Directory -Force -Path $BackupDirectory | Out-Null
$Timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$DatabaseBackup = Join-Path $BackupDirectory "dockside-$Timestamp.sql"

Write-Host "Creating a pre-upgrade PostgreSQL backup at $DatabaseBackup"
$BackupProcess = Start-Process -FilePath "docker" -ArgumentList @(
    "compose", "--env-file", ".env", "exec", "-T", "postgres",
    "pg_dump", "-U", "dockside", "-d", "dockside", "--clean", "--if-exists"
) -NoNewWindow -Wait -PassThru -RedirectStandardOutput $DatabaseBackup
if ($BackupProcess.ExitCode -ne 0) {
    Remove-Item -LiteralPath $DatabaseBackup -Force -ErrorAction SilentlyContinue
    throw "Database backup failed; the upgrade was not started."
}

if ($Version) {
    $Lines = Get-Content -LiteralPath ".env"
    $Updated = $false
    $Lines = $Lines | ForEach-Object {
        if ($_ -match "^DOCKSIDE_VERSION=") {
            $Updated = $true
            "DOCKSIDE_VERSION=$Version"
        } else {
            $_
        }
    }
    if (-not $Updated) { $Lines += "DOCKSIDE_VERSION=$Version" }
    [IO.File]::WriteAllLines(
        (Join-Path $ProjectRoot ".env"),
        $Lines,
        (New-Object Text.UTF8Encoding($false))
    )
}

if ($BuildFromSource -or $Version -eq "dev") {
    & docker compose --env-file .env up -d --build
} else {
    & docker compose --env-file .env pull
    if ($LASTEXITCODE -ne 0) { throw "Image pull failed; current containers remain available." }
    & docker compose --env-file .env up -d --no-build
}
if ($LASTEXITCODE -ne 0) { throw "Compose could not apply the upgrade." }

$Ready = $false
for ($attempt = 0; $attempt -lt 60; $attempt++) {
    & docker compose --env-file .env exec -T app /dockside healthcheck http://127.0.0.1:8080/health/ready 2>$null
    if ($LASTEXITCODE -eq 0) { $Ready = $true; break }
    Start-Sleep -Seconds 2
}
if (-not $Ready) {
    & docker compose --env-file .env ps
    throw "The upgraded app did not become ready. The database backup is $DatabaseBackup."
}

Write-Host "Dockside upgrade completed. Database backup: $DatabaseBackup" -ForegroundColor Green
