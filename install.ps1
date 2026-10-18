# Installs docker-buildx-cache as the current user's docker-buildx (Windows),
# from a release: the latest, or the given one, checked against its
# SHA256SUMS.
#
# Usage: install.ps1 [-Version <version, e.g. v0.1.0>]
param([string]$Version = "latest")
$ErrorActionPreference = "Stop"

$Repo = "geckosoft-labs/docker-buildx-cache"
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$base = if ($Version -eq "latest") {
  "https://github.com/$Repo/releases/latest/download"
} else {
  "https://github.com/$Repo/releases/download/$Version"
}
$asset = "docker-buildx-cache-windows-$arch.exe"

$config = if ($env:DOCKER_CONFIG) { $env:DOCKER_CONFIG } else { Join-Path $env:USERPROFILE ".docker" }
$target = Join-Path $config "cli-plugins\docker-buildx.exe"
$real = Join-Path $config "docker-buildx-cache\docker-buildx.exe"

$tmp = New-Item -ItemType Directory -Path (Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid()))
try {
  Invoke-WebRequest "$base/$asset" -OutFile (Join-Path $tmp $asset) -UseBasicParsing
  Invoke-WebRequest "$base/SHA256SUMS" -OutFile (Join-Path $tmp "SHA256SUMS") -UseBasicParsing
  $line = Get-Content (Join-Path $tmp "SHA256SUMS") | Where-Object { $_ -match " $([regex]::Escape($asset))$" }
  if (-not $line) { throw "$asset is not in the release's SHA256SUMS" }
  $want = ($line -split '\s+')[0]
  $got = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
  if ($got -ne $want) { throw "Wrong checksum for $asset`: $got, SHA256SUMS has $want" }

  # A docker-buildx.exe already in the user's directory that isn't the
  # wrapper is the real one: it moves aside, where the wrapper finds it. The
  # real one rejects the argument, which is the answer looked for, so its
  # error is neither shown nor raised.
  if (Test-Path $target) {
    $ErrorActionPreference = "Continue"
    & $target docker-buildx-cache-version *> $null
    $isWrapper = $LASTEXITCODE -eq 0
    $ErrorActionPreference = "Stop"
    if (-not $isWrapper) {
      if (Test-Path $real) { throw "Both $target and $real exist: remove the one that isn't the real docker-buildx" }
      New-Item -ItemType Directory -Force -Path (Split-Path $real) | Out-Null
      Move-Item $target $real
      Write-Output "Moved the real docker-buildx to $real"
    }
  }

  New-Item -ItemType Directory -Force -Path (Split-Path $target) | Out-Null
  Move-Item -Force (Join-Path $tmp $asset) $target
  Write-Output "Installed $(& $target docker-buildx-cache-version) as $target"
} finally {
  Remove-Item -Recurse -Force $tmp
}

# Checks that the wrapper finds the real docker-buildx
docker buildx version
if ($LASTEXITCODE -ne 0) { throw "docker buildx version failed" }
