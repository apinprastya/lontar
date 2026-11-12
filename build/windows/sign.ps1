# Authenticode-signs Windows release files (CI only).
#
#   pwsh build/windows/sign.ps1 bin/dbedit.exe [more files...]
#
# Inputs come from the environment (GitHub secrets); nothing is stored in the
# repository:
#   WINDOWS_CERT_PFX        base64 of a code-signing .pfx
#   WINDOWS_CERT_PASSWORD   its password
#   WINDOWS_TIMESTAMP_URL   optional RFC 3161 server (default DigiCert)
# The release workflow only calls this when both secrets are set; without them
# the exe and the installer are unsigned (SmartScreen warns on first run).
param([Parameter(Mandatory = $true, ValueFromRemainingArguments = $true)][string[]]$Files)
$ErrorActionPreference = 'Stop'

if (-not $env:WINDOWS_CERT_PFX -or -not $env:WINDOWS_CERT_PASSWORD) {
    throw 'sign.ps1: WINDOWS_CERT_PFX and WINDOWS_CERT_PASSWORD must be set'
}
$timestamp = if ($env:WINDOWS_TIMESTAMP_URL) { $env:WINDOWS_TIMESTAMP_URL } else { 'http://timestamp.digicert.com' }

# Newest signtool from the Windows SDK on the runner.
$signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
    Sort-Object FullName -Descending | Select-Object -First 1
if (-not $signtool) { throw 'sign.ps1: signtool.exe not found (Windows 10 SDK)' }

$pfx = Join-Path ([System.IO.Path]::GetTempPath()) ("dbedit-" + [guid]::NewGuid() + ".pfx")
try {
    [System.IO.File]::WriteAllBytes($pfx, [Convert]::FromBase64String($env:WINDOWS_CERT_PFX))
    foreach ($f in $Files) {
        & $signtool.FullName sign /fd SHA256 /f $pfx /p $env:WINDOWS_CERT_PASSWORD /tr $timestamp /td SHA256 /d 'dbedit' $f
        if ($LASTEXITCODE -ne 0) { throw "sign.ps1: signing $f failed" }
        & $signtool.FullName verify /pa $f
        if ($LASTEXITCODE -ne 0) { throw "sign.ps1: verifying $f failed" }
    }
} finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $pfx
}
