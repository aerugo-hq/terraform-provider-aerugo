# Development only: registers the development realm's kl-terraform client as an org-admin service account on your
# local Aerugo (make up in the platform's repository), so the provider and its acceptance tests can sign in.
#
#   .\scripts\dev-service-account.ps1
#
# It asks for an org admin's password (olivia in the development realm), signs in as them, and registers the client.
# The platform's smoke checks remove this registration when they finish, so run this again after make smoke.
param(
  [string]$Api = "http://localhost:8080",
  [string]$TokenUrl = "http://localhost:8081/realms/acme/protocol/openid-connect/token",
  [string]$Admin = "olivia"
)
$ErrorActionPreference = "Stop"

foreach ($address in @($Api, $TokenUrl)) {
  $hostname = ([uri]$address).Host
  if ($hostname -ne "localhost" -and $hostname -ne "127.0.0.1") {
    throw "This script only sets up a local development stack, not $address."
  }
}

$credential = Get-Credential -UserName $Admin -Message "An org admin in your development realm"
$signIn = Invoke-RestMethod -Method Post -Uri $TokenUrl -Body @{
  grant_type = "password"
  client_id  = "kl-cli"
  username   = $credential.UserName
  password   = $credential.GetNetworkCredential().Password
}
$headers = @{ Authorization = "Bearer $($signIn.access_token)" }
$body = @{ client_id = "kl-terraform"; name = "Terraform (development)"; org_admin = $true } | ConvertTo-Json

try {
  Invoke-RestMethod -Method Post -Uri "$Api/v1/service-accounts" -Headers $headers -ContentType "application/json" -Body $body | Out-Null
  Write-Host "Registered kl-terraform as an org-admin service account."
} catch {
  if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 409) {
    Write-Host "kl-terraform is already registered."
  } else {
    throw
  }
}

Write-Host ""
Write-Host "For this terminal, so the provider can sign in:"
Write-Host "  `$env:AERUGO_ENDPOINT = `"$Api`""
Write-Host "  `$env:AERUGO_TOKEN_URL = `"$TokenUrl`""
Write-Host "  `$env:AERUGO_CLIENT_ID = `"kl-terraform`""
Write-Host "  `$env:AERUGO_CLIENT_SECRET = `"kl-terraform-dev-secret`"   # the development realm's, never a real one"
