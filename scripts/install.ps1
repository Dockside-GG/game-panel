[CmdletBinding()]
param(
    [ValidateSet("local", "proxy", "public")]
    [string]$Mode,
    [string]$PublicUrl,
    [string]$DiscordClientId,
    [string]$AcmeEmail,
    [int]$ListenPort,
    [ValidateSet("administrators", "operators", "everyone", "off")]
    [string]$MfaPolicy,
    [int]$GamePortStart,
    [int]$GamePortEnd,
    [string]$Version,
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$ProjectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$EnvPath = Join-Path $ProjectRoot ".env"
$SecretsPath = Join-Path $ProjectRoot "secrets"
$GeneratedPath = Join-Path $ProjectRoot "deploy\generated"

function Read-Choice {
    param([string]$Prompt, [string[]]$Options, [int]$Default = 0)
    while ($true) {
        Write-Host ""
        Write-Host $Prompt
        for ($index = 0; $index -lt $Options.Count; $index++) {
            $suffix = if ($index -eq $Default) { " [default]" } else { "" }
            Write-Host "  $($index + 1)) $($Options[$index])$suffix"
        }
        $answer = Read-Host "Select $($Default + 1)"
        if ([string]::IsNullOrWhiteSpace($answer)) {
            return $Default
        }
        $parsed = 0
        if ([int]::TryParse($answer, [ref]$parsed) -and $parsed -ge 1 -and $parsed -le $Options.Count) {
            return $parsed - 1
        }
    }
}

function Read-Default {
    param([string]$Prompt, [string]$Default)
    $answer = Read-Host "$Prompt [$Default]"
    if ([string]::IsNullOrWhiteSpace($answer)) { return $Default }
    return $answer.Trim()
}

function Read-SecretText {
    param([string]$Prompt)
    if ($env:DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET) {
        return $env:DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET.Trim()
    }
    $secure = Read-Host $Prompt -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

function New-RandomToken {
    param([int]$Bytes)
    $buffer = New-Object byte[] $Bytes
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($buffer) } finally { $generator.Dispose() }
    return [Convert]::ToBase64String($buffer).TrimEnd("=").Replace("+", "-").Replace("/", "_")
}

function Write-Utf8File {
    param([string]$Path, [string]$Value)
    [IO.File]::WriteAllText($Path, $Value, (New-Object Text.UTF8Encoding($false)))
}

function Assert-Command {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "$Command $($Arguments -join ' ') failed. Ensure Docker Desktop is installed, running, and using Linux containers."
    }
}

Set-Location $ProjectRoot
Write-Host ""
Write-Host "Dockside.GG Game Panel guided installer" -ForegroundColor Cyan
Write-Host "This installer creates local secrets and starts the Docker Compose stack."

if (Test-Path -LiteralPath $EnvPath) {
    throw ".env already exists. This installer will not overwrite an installation. Use scripts\upgrade.ps1 instead."
}
Assert-Command "docker" @("version")
Assert-Command "docker" @("compose", "version")

if (-not $Mode) {
    $modeIndex = Read-Choice "How will the panel be reached?" @(
        "Local computer only (HTTP on localhost)",
        "Behind an existing Nginx/Caddy/Apache site (recommended when this host serves other websites)",
        "Directly from the internet with Dockside Caddy managing HTTPS"
    ) 1
    $Mode = @("local", "proxy", "public")[$modeIndex]
}

$BindAddress = "127.0.0.1"
$HttpPort = 8080
$HttpsPort = 8443
$CaddyFile = "./deploy/caddy/Caddyfile"
$SecureCookies = "true"

switch ($Mode) {
    "local" {
        $HttpPort = if ($ListenPort -gt 0) { $ListenPort } else { [int](Read-Default "Local panel port" "8080") }
        if (-not $PublicUrl) { $PublicUrl = "http://localhost:$HttpPort" }
        $SecureCookies = "false"
    }
    "proxy" {
        $HttpPort = if ($ListenPort -gt 0) { $ListenPort } else { [int](Read-Default "Loopback upstream port for your existing reverse proxy" "8080") }
        if (-not $PublicUrl) { $PublicUrl = Read-Host "Exact external panel URL (example: https://panel.example.com)" }
    }
    "public" {
        $BindAddress = "0.0.0.0"
        $HttpPort = 80
        $HttpsPort = 443
        $CaddyFile = "./deploy/caddy/Caddyfile.public"
        if (-not $PublicUrl) { $PublicUrl = Read-Host "Exact public panel URL (example: https://panel.example.com)" }
        if (-not $AcmeEmail) { $AcmeEmail = Read-Host "Email for Let's Encrypt/ACME notices" }
        if ($AcmeEmail -notmatch "^[^@\s]+@[^@\s]+\.[^@\s]+$") {
            throw "Direct public TLS mode requires a valid ACME contact email."
        }
    }
}

try { $PanelUri = [Uri]$PublicUrl.TrimEnd("/") } catch { throw "The panel URL is invalid." }
if (-not $PanelUri.IsAbsoluteUri -or $PanelUri.AbsolutePath -ne "/" -or $PanelUri.Query -or $PanelUri.Fragment) {
    throw "The panel URL must contain only scheme, host, and optional port; path prefixes are not supported."
}
if ($Mode -eq "local") {
    if ($PanelUri.Scheme -ne "http" -or $PanelUri.Host -notin @("localhost", "127.0.0.1", "::1")) {
        throw "Local mode requires a loopback HTTP URL."
    }
    if ($PanelUri.Port -ne $HttpPort) {
        throw "The local panel URL port must match the selected listener port ($HttpPort)."
    }
} elseif ($PanelUri.Scheme -ne "https") {
    throw "External and reverse-proxy modes require HTTPS."
}
if ($Mode -eq "public" -and (
    $PanelUri.IsLoopback -or
    $PanelUri.Host -match "^\d{1,3}(?:\.\d{1,3}){3}$" -or
    $PanelUri.Host.Contains(":")
)) {
    throw "Direct public TLS mode requires a DNS hostname, not localhost or a raw IP address."
}
$PublicUrl = $PanelUri.GetLeftPart([UriPartial]::Authority)
$Hostname = $PanelUri.DnsSafeHost
$RedirectUri = "$PublicUrl/api/v1/auth/discord/callback"
$ComposeFiles = if ($Mode -eq "public") { "compose.yml;compose.public.yml" } else { "compose.yml" }

Write-Host ""
Write-Host "Discord application setup" -ForegroundColor Cyan
Write-Host "  1. Open https://discord.com/developers/applications and create/select an application."
Write-Host "  2. In OAuth2, add this exact Redirect URI:"
Write-Host "     $RedirectUri" -ForegroundColor Yellow
Write-Host "  3. A bot is not required. Dockside requests only the OAuth2 identify scope."
Write-Host "  4. Copy the Application ID and Client Secret below."
if (-not $DiscordClientId) { $DiscordClientId = Read-Host "Discord Application (Client) ID" }
if ($DiscordClientId -notmatch "^[0-9]{15,25}$") { throw "The Discord Client ID must be numeric." }
$DiscordClientSecret = Read-SecretText "Discord Client Secret"
if ([string]::IsNullOrWhiteSpace($DiscordClientSecret)) { throw "The Discord Client Secret cannot be empty." }

if (-not $MfaPolicy) {
    $mfaIndex = Read-Choice "Which Discord users must have MFA enabled?" @(
        "Owners and administrators",
        "Administrators and operators",
        "Everyone",
        "Do not require Discord MFA"
    ) 0
    $MfaPolicy = @("administrators", "operators", "everyone", "off")[$mfaIndex]
}

if ($GamePortStart -eq 0) { $GamePortStart = [int](Read-Default "First game-server host port" "20000") }
if ($GamePortEnd -eq 0) { $GamePortEnd = [int](Read-Default "Last game-server host port" "29999") }
if ($GamePortStart -lt 1024 -or $GamePortEnd -gt 65535 -or $GamePortStart -gt $GamePortEnd) {
    throw "The game port range must be between 1024 and 65535."
}
if (-not $Version) { $Version = Read-Default "Panel image version (use dev to build this checkout)" "dev" }
if ($Version -notmatch "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$") {
    throw "The panel image version contains unsupported characters."
}

New-Item -ItemType Directory -Force -Path $SecretsPath, $GeneratedPath, (Join-Path $ProjectRoot "data\servers"), (Join-Path $ProjectRoot "data\backups") | Out-Null
$PostgresPassword = New-RandomToken 36
$EncryptionKey = New-RandomToken 32
$SessionKey = New-RandomToken 32
$EngineToken = New-RandomToken 48
$BootstrapToken = New-RandomToken 32
$InstanceId = [Guid]::NewGuid().ToString()

Write-Utf8File (Join-Path $SecretsPath "postgres_password") $PostgresPassword
Write-Utf8File (Join-Path $SecretsPath "database_url") "postgres://dockside:$PostgresPassword@postgres:5432/dockside?sslmode=disable"
Write-Utf8File (Join-Path $SecretsPath "encryption_key") $EncryptionKey
Write-Utf8File (Join-Path $SecretsPath "session_key") $SessionKey
Write-Utf8File (Join-Path $SecretsPath "discord_client_secret") $DiscordClientSecret
Write-Utf8File (Join-Path $SecretsPath "bootstrap_token") $BootstrapToken
Write-Utf8File (Join-Path $SecretsPath "engine_token") $EngineToken

$CurrentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
& icacls $SecretsPath /inheritance:r /grant:r "$CurrentIdentity`:(OI)(CI)F" "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null

$Environment = @"
COMPOSE_PROJECT_NAME=dockside
COMPOSE_FILE=$ComposeFiles
DOCKSIDE_VERSION=$Version
DOCKSIDE_INSTANCE_ID=$InstanceId
DOCKSIDE_PUBLIC_URL=$PublicUrl
DOCKSIDE_HOSTNAME=$Hostname
DOCKSIDE_ACME_EMAIL=$AcmeEmail
DOCKSIDE_CADDYFILE=$CaddyFile
DOCKSIDE_BIND_ADDRESS=$BindAddress
DOCKSIDE_HTTP_PORT=$HttpPort
DOCKSIDE_HTTPS_PORT=$HttpsPort
DOCKSIDE_POSTGRES_DB=dockside
DOCKSIDE_POSTGRES_USER=dockside
DOCKSIDE_DISCORD_CLIENT_ID=$DiscordClientId
DOCKSIDE_MFA_POLICY=$MfaPolicy
DOCKSIDE_GAME_PORT_START=$GamePortStart
DOCKSIDE_GAME_PORT_END=$GamePortEnd
DOCKSIDE_SERVER_UID=1000
DOCKSIDE_SERVER_GID=1000
DOCKSIDE_DOCKER_GID=0
DOCKSIDE_HOST_DATA_ROOT=./data/servers
DOCKSIDE_HOST_BACKUP_ROOT=./data/backups
DOCKSIDE_LOG_LEVEL=info
DOCKSIDE_SECURE_COOKIES=$SecureCookies
"@
Write-Utf8File $EnvPath $Environment

if ($Mode -eq "proxy") {
    $NginxConfig = @"
map `$http_upgrade `$dockside_connection_upgrade {
    default upgrade;
    "" close;
}

server {
    listen 443 ssl http2;
    server_name $Hostname;

    # Keep your existing certificate directives for this hostname here.
    client_max_body_size 2g;

    location / {
        proxy_pass http://127.0.0.1:$HttpPort;
        proxy_http_version 1.1;
        proxy_set_header Host `$host;
        proxy_set_header X-Forwarded-Proto `$scheme;
        proxy_set_header X-Forwarded-For `$proxy_add_x_forwarded_for;
        proxy_set_header X-Real-IP `$remote_addr;
        proxy_set_header Upgrade `$http_upgrade;
        proxy_set_header Connection `$dockside_connection_upgrade;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
"@
    Write-Utf8File (Join-Path $GeneratedPath "nginx-dockside.conf") $NginxConfig
}

Write-Host ""
Write-Host "Validating generated Compose configuration..."
& docker compose --env-file .env config --quiet
if ($LASTEXITCODE -ne 0) { throw "Docker Compose rejected the generated configuration." }

if (-not $NoStart) {
    Write-Host "Starting Dockside containers. Initial image downloads can take several minutes..."
    if ($Version -eq "dev") {
        & docker compose --env-file .env up -d --build
    } else {
        & docker compose --env-file .env pull
        if ($LASTEXITCODE -ne 0) { throw "Could not pull Dockside images." }
        & docker compose --env-file .env up -d --no-build
    }
    if ($LASTEXITCODE -ne 0) { throw "Dockside did not start successfully." }
    $Ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        & docker compose --env-file .env exec -T app /dockside healthcheck http://127.0.0.1:8080/health/ready 2>$null
        if ($LASTEXITCODE -eq 0) { $Ready = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $Ready) {
        & docker compose --env-file .env ps
        throw "The panel did not become ready. Run docker compose logs app worker engine postgres."
    }
}

Write-Host ""
Write-Host "Dockside installation is ready." -ForegroundColor Green
Write-Host "Panel URL: $PublicUrl"
Write-Host "Discord Redirect URI: $RedirectUri"
Write-Host "One-time owner bootstrap token: $BootstrapToken" -ForegroundColor Yellow
Write-Host "Open the panel, choose the owner-claim flow, and enter that token."
if ($Mode -eq "proxy") {
    Write-Host "Generated Nginx vhost: deploy\generated\nginx-dockside.conf"
    Write-Host "Add only that vhost to Nginx after configuring its certificate; Dockside remains bound to 127.0.0.1:$HttpPort."
}
if ($Mode -eq "public") {
    Write-Host "Confirm DNS for $Hostname points to this host and inbound TCP 80/443 are allowed."
}
Write-Host "Game-server ports to allow as needed: $GamePortStart-$GamePortEnd (TCP/UDP depends on each template)."
