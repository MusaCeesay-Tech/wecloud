# WeCloud leader node bootstrap (Windows)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root
$domain = $env:WECLOUD_BASE_DOMAIN
$pubUrl = $env:WECLOUD_PUBLIC_URL
$proxyPort = if ($env:WECLOUD_PROXY_PORT) { $env:WECLOUD_PROXY_PORT } else { '8081' }
$scheme = if ($env:WECLOUD_PUBLIC_SCHEME) { $env:WECLOUD_PUBLIC_SCHEME } else { 'http' }
if (-not $domain) { Write-Error 'WECLOUD_BASE_DOMAIN not set'; exit 1 }
if (-not $pubUrl) { $pubUrl = "$scheme://$domain" }
$envFile = '.env.leader'
@"
WECLOUD_BASE_DOMAIN=$domain
WECLOUD_PUBLIC_SCHEME=$scheme
WECLOUD_PROXY_PORT=$proxyPort
WECLOUD_PUBLIC_URL=$pubUrl
WECLOUD_REWARD_INTERVAL=60
WECLOUD_REWARD_SECONDS_PER_CREDIT=3600
"@ | Set-Content -Path $envFile
Write-Host 'Starting WeCloud leader...'
docker compose -f deploy/leader.docker-compose.yml --env-file $envFile up -d --build
Write-Host 'Leader running: coordinator :8080, proxy :8081, web :3000'
Write-Host "Base domain: $domain"
Write-Host "Public URL:  $pubUrl"
Write-Host "App pattern: <app-id>.$domain:$proxyPort"