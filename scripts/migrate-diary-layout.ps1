# Migrates diary entries from the old layout  <Root>\YYYY\MM\entry-DD-HH MM SS.txt
# to the day-folder layout                    <Root>\YYYY\MM\DD\entry-HH-MM-SS.txt
# Time separators in old names may be ':', space or '-'. Files already in day
# folders are ignored. Existing destinations are never overwritten.
# Use -WhatIf to print the planned moves without changing anything.
param(
    [string]$Root = 'G:\My Drive\diary',
    [switch]$WhatIf
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
    Write-Error "Diary root not found: $Root"
    exit 1
}
$Root = (Resolve-Path -LiteralPath $Root).Path.TrimEnd('\')
$pattern = '^entry-(\d\d)-(\d\d)[ :-](\d\d)[ :-](\d\d)\.txt$'
$moved = 0; $skipped = 0

foreach ($yearDir in Get-ChildItem -LiteralPath $Root -Directory | Where-Object { $_.Name -match '^\d{4}$' } | Sort-Object Name) {
    foreach ($monthDir in Get-ChildItem -LiteralPath $yearDir.FullName -Directory | Where-Object { $_.Name -match '^\d\d$' } | Sort-Object Name) {
        foreach ($file in Get-ChildItem -LiteralPath $monthDir.FullName -File | Sort-Object Name) {
            if ($file.Name -notmatch $pattern) { continue }
            $dd, $hh, $mi, $ss = $Matches[1], $Matches[2], $Matches[3], $Matches[4]
            $newDir = Join-Path $monthDir.FullName $dd
            $newName = "entry-$hh-$mi-$ss.txt"
            $dest = Join-Path $newDir $newName
            $rel = "$($yearDir.Name)\$($monthDir.Name)"
            $line = "$rel\$($file.Name) -> $rel\$dd\$newName"
            if (Test-Path -LiteralPath $dest) {
                Write-Warning "SKIP (destination exists): $line"
                $skipped++
                continue
            }
            Write-Output $line
            if (-not $WhatIf) {
                if (-not (Test-Path -LiteralPath $newDir)) { New-Item -ItemType Directory -Path $newDir | Out-Null }
                Move-Item -LiteralPath $file.FullName -Destination $dest
            }
            $moved++
        }
    }
}
$verb = if ($WhatIf) { 'planned' } else { 'moved' }
Write-Output "$moved $verb, $skipped skipped"
