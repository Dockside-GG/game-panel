[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Version,
    [string]$OutputDirectory = "artifacts"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$SemVerPattern = "^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$"
if ($Version -notmatch $SemVerPattern) {
    throw "Version must be Semantic Versioning without a v prefix."
}

$ProjectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$OutputRoot = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    [IO.Path]::GetFullPath($OutputDirectory)
} else {
    [IO.Path]::GetFullPath((Join-Path $ProjectRoot $OutputDirectory))
}
$PackageName = "dockside-game-panel-$Version"
$StagingParent = Join-Path $OutputRoot (".package-" + [Guid]::NewGuid())
$StagingRoot = Join-Path $StagingParent $PackageName
$ZipPath = Join-Path $OutputRoot "$PackageName.zip"
$TarPath = Join-Path $OutputRoot "$PackageName.tar.gz"
$ChecksumPath = Join-Path $OutputRoot "SHA256SUMS"

$Package = Get-Content -Raw (Join-Path $ProjectRoot "web\package.json") | ConvertFrom-Json
if ($Package.version -ne $Version) {
    throw "web/package.json is version $($Package.version); expected $Version."
}
$Changelog = [IO.File]::ReadAllText((Join-Path $ProjectRoot "CHANGELOG.md"))
if (-not $Changelog.Contains("## [$Version]")) {
    throw "CHANGELOG.md does not contain a [$Version] release heading."
}

New-Item -ItemType Directory -Force -Path $OutputRoot, $StagingRoot | Out-Null
try {
    foreach ($File in @(
        ".env.example",
        "CHANGELOG.md",
        "compose.yml",
        "compose.public.yml",
        "CONTRIBUTING.md",
        "LICENSE",
        "NOTICE",
        "README.md"
    )) {
        Copy-Item -LiteralPath (Join-Path $ProjectRoot $File) -Destination $StagingRoot
    }
    foreach ($Directory in @("deploy", "docs", "scripts")) {
        Copy-Item -LiteralPath (Join-Path $ProjectRoot $Directory) -Destination $StagingRoot -Recurse
    }
    [IO.File]::WriteAllText(
        (Join-Path $StagingRoot ".dockside-release"),
        "$Version`n",
        (New-Object Text.UTF8Encoding($false))
    )

    if (Test-Path -LiteralPath $ZipPath) { Remove-Item -LiteralPath $ZipPath -Force }
    if (Test-Path -LiteralPath $TarPath) { Remove-Item -LiteralPath $TarPath -Force }
    Compress-Archive -LiteralPath $StagingRoot -DestinationPath $ZipPath -CompressionLevel Optimal
    if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
        throw "tar.exe is required to create the release tarball."
    }
    & tar.exe -czf $TarPath -C $StagingParent $PackageName
    if ($LASTEXITCODE -ne 0) { throw "tar.exe could not create the release tarball." }

    $Lines = @($ZipPath, $TarPath) | ForEach-Object {
        $Hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_).Hash.ToLowerInvariant()
        "$Hash  $([IO.Path]::GetFileName($_))"
    }
    [IO.File]::WriteAllLines($ChecksumPath, $Lines, (New-Object Text.UTF8Encoding($false)))
    Write-Host "Release bundle created in $OutputRoot" -ForegroundColor Green
} finally {
    if (Test-Path -LiteralPath $StagingParent) {
        Remove-Item -LiteralPath $StagingParent -Recurse -Force
    }
}
