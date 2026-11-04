$ErrorActionPreference = 'Stop'
$base = 'https://releases.flashmind.com/teacli'
if ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64') { throw 'No teacli binary is available for this platform.' }
$tag = (Invoke-RestMethod "$base/latest.txt").Trim()
if ($tag -notmatch '^v\d+\.\d+\.\d+$') { throw 'Invalid release tag.' }
$name = 'teacli-windows-amd64.exe'
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
try {
    Invoke-WebRequest "$base/$tag/$name" -OutFile $tmp
    $sums = Invoke-RestMethod "$base/$tag/checksums.txt"
    $entry = ($sums -split "`n" | Where-Object { $_ -match "^[0-9a-fA-F]{64}\s+\*?$name\s*$" } | Select-Object -First 1)
    if (-not $entry -or (Get-FileHash -Algorithm SHA256 $tmp).Hash -ne ($entry -split '\s+')[0]) { throw 'Checksum mismatch or missing checksum.' }
    $dir = if ($env:TEACLI_INSTALL_DIR) { $env:TEACLI_INSTALL_DIR } else { Join-Path $env:USERPROFILE '.local\bin' }
    New-Item -ItemType Directory -Force $dir | Out-Null
    Move-Item -Force $tmp (Join-Path $dir 'teacli.exe')
    Write-Host "Installed teacli $tag to $dir\teacli.exe"
    Write-Host "Add $dir to PATH if it is not already there."
} finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $tmp
}
