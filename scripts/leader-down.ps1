# WeCloud leader node shutdown (Windows)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root
docker compose -f deploy/leader.docker-compose.yml down -v --remove-orphans 2>$null | Out-Null
docker compose -f deploy/leader.docker-compose.yml down --remove-orphans
Write-Host 'WeCloud leader stopped.'