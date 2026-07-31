[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$SourceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$TestRoot = Join-Path $SourceRoot (".tools\windows-installer-smoke-" + [Guid]::NewGuid())

function global:docker {
    $global:LASTEXITCODE = 0
}

function Assert-Line {
    param([string]$Path, [string]$Expected)
    $lines = [IO.File]::ReadAllLines($Path)
    if ($Expected -notin $lines) {
        throw "Expected '$Expected' in $Path."
    }
}

function Invoke-InstallerCase {
    param([string]$Name, [hashtable]$InstallerArguments)
    $caseRoot = Join-Path $TestRoot $Name
    $scriptsRoot = Join-Path $caseRoot "scripts"
    New-Item -ItemType Directory -Force -Path $scriptsRoot | Out-Null
    Copy-Item -LiteralPath (Join-Path $SourceRoot "scripts\install.ps1") -Destination $scriptsRoot
    $env:DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET = "$Name-test-secret"
    & (Join-Path $scriptsRoot "install.ps1") @InstallerArguments -NoStart *> $null
    return $caseRoot
}

try {
    New-Item -ItemType Directory -Force -Path $TestRoot | Out-Null

    $localRoot = Invoke-InstallerCase "local" @{
        Mode = "local"
        PublicUrl = "http://localhost:18088"
        ListenPort = 18088
        DiscordClientId = "123456789012345678"
        MfaPolicy = "administrators"
        GamePortStart = 20000
        GamePortEnd = 20099
        Version = "dev"
    }
    Assert-Line (Join-Path $localRoot ".env") "COMPOSE_FILE=compose.yml"
    Assert-Line (Join-Path $localRoot ".env") "DOCKSIDE_PUBLIC_URL=http://localhost:18088"
    Assert-Line (Join-Path $localRoot ".env") "DOCKSIDE_BIND_ADDRESS=127.0.0.1"
    Assert-Line (Join-Path $localRoot ".env") "DOCKSIDE_HTTP_PORT=18088"
    Assert-Line (Join-Path $localRoot ".env") "DOCKSIDE_SECURE_COOKIES=false"

    $proxyRoot = Invoke-InstallerCase "proxy" @{
        Mode = "proxy"
        PublicUrl = "https://panel.example.test"
        ListenPort = 18089
        DiscordClientId = "123456789012345678"
        MfaPolicy = "operators"
        GamePortStart = 21000
        GamePortEnd = 21099
        Version = "dev"
    }
    Assert-Line (Join-Path $proxyRoot ".env") "COMPOSE_FILE=compose.yml"
    Assert-Line (Join-Path $proxyRoot ".env") "DOCKSIDE_PUBLIC_URL=https://panel.example.test"
    $nginx = [IO.File]::ReadAllText((Join-Path $proxyRoot "deploy\generated\nginx-dockside.conf"))
    if (-not $nginx.Contains("server_name panel.example.test;") -or -not $nginx.Contains("proxy_pass http://127.0.0.1:18089;")) {
        throw "The Windows installer generated an invalid Nginx vhost."
    }

    $publicRoot = Invoke-InstallerCase "public" @{
        Mode = "public"
        PublicUrl = "https://panel.example.test"
        DiscordClientId = "123456789012345678"
        AcmeEmail = "ops@example.test"
        MfaPolicy = "everyone"
        GamePortStart = 22000
        GamePortEnd = 22099
        Version = "v1.0.0"
    }
    Assert-Line (Join-Path $publicRoot ".env") "COMPOSE_FILE=compose.yml;compose.public.yml"
    Assert-Line (Join-Path $publicRoot ".env") "DOCKSIDE_BIND_ADDRESS=0.0.0.0"
    Assert-Line (Join-Path $publicRoot ".env") "DOCKSIDE_HTTP_PORT=80"
    Assert-Line (Join-Path $publicRoot ".env") "DOCKSIDE_HTTPS_PORT=443"

    Write-Output "Windows guided installer smoke tests passed."
} finally {
    Set-Location $SourceRoot
    Remove-Item Env:\DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET -ErrorAction SilentlyContinue
    Remove-Item Function:\docker -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $TestRoot) {
        $resolvedTestRoot = [IO.Path]::GetFullPath($TestRoot)
        if (-not $resolvedTestRoot.StartsWith($SourceRoot + [IO.Path]::DirectorySeparatorChar)) {
            throw "Refusing to remove unexpected test path: $resolvedTestRoot"
        }
        & icacls $resolvedTestRoot /reset /T /C | Out-Null
        Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force
    }
}
