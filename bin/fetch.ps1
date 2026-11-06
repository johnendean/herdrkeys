# Install step on Windows: fetch this release's binary and check it.
#
# Herdr runs this from [[build]] after cloning the plugin. It downloads the
# release asset whose tag is the manifest's `version`, and installs it only if
# its SHA-256 matches checksums.txt in this commit. A checkout that already has
# a binary (a developer's `go build`) is left alone. Uses only what ships with
# Windows PowerShell 5.1. HERDRKEYS_RELEASE_URL replaces the download base,
# for a mirror or for testing. See ADR 0020.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'  # the progress bar makes downloads crawl

function Fail($message) {
    [Console]::Error.WriteLine("herdrkeys: $message")
    exit 1
}

$root = Split-Path -Parent $PSScriptRoot
$dest = Join-Path $root 'bin\herdrkeys.exe'
$repo = 'johnendean/herdrkeys'

# Start the daemon now, rather than waiting for the next server start to fire
# the startup hook: boards that stay dark after install look like a broken
# install. A start from this build takes over from any older one that is
# running. A failure here never fails the install.
function FirstStart {
    if ($env:HERDRKEYS_NO_START) { return }
    & $dest start
    if ($LASTEXITCODE -ne 0) {
        [Console]::Error.WriteLine('herdrkeys: could not start the daemon; the startup hook will, on next start')
    }
}

if (Test-Path $dest) {
    Write-Output "herdrkeys: $dest already exists; not downloading"
    FirstStart
    exit 0
}

$manifest = Get-Content (Join-Path $root 'herdr-plugin.toml')
$version = $null
foreach ($line in $manifest) {
    if ($line -match '^version\s*=\s*"([^"]+)"') { $version = $Matches[1]; break }
}
if (-not $version) { Fail 'no version in herdr-plugin.toml' }

switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Fail "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}

$asset = "herdrkeys-windows-$arch.exe"
$base = $env:HERDRKEYS_RELEASE_URL
if (-not $base) { $base = "https://github.com/$repo/releases/download/v$version" }
$url = "$base/$asset"

$want = $null
foreach ($line in Get-Content (Join-Path $root 'checksums.txt')) {
    $fields = $line -split '\s+'
    if ($fields.Count -ge 2 -and $fields[1] -eq $asset) { $want = $fields[0].ToLowerInvariant(); break }
}
if (-not $want) { Fail "no checksum for $asset in checksums.txt" }

$tmp = "$dest.download"
try {
    Write-Output "herdrkeys: downloading $url"
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $tmp
    } catch {
        Fail "download failed: $url ($($_.Exception.Message))"
    }

    $got = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash.ToLowerInvariant()
    if ($got -ne $want) {
        Fail "checksum mismatch for ${asset}: expected $want, got $got; not installing"
    }
    Move-Item -Force $tmp $dest
} finally {
    if (Test-Path $tmp) { Remove-Item -Force $tmp }
}
Write-Output "herdrkeys: installed $asset v$version"
FirstStart
exit 0
