<#
.SYNOPSIS
  Converts a video (typically a 4K HEVC .mkv) into an MP4 that plays in every
  phone/tablet/desktop browser, and VERIFIES the result against the source.

.DESCRIPTION
  Output: H.264 High 8-bit at -Height (default 1080p), one stereo AAC audio
  track, all text subtitle tracks as mov_text, faststart. The original is
  never touched or deleted.

  Design notes (see web-svc/NOTES.md, "Converting videos for browsers"):
   * Video+subtitles and audio are encoded in SEPARATE passes and then muxed
     with -c copy. A single combined ffmpeg run once silently cut the audio at
     43% of a film while exiting 0.
   * A clean exit code proves nothing, so the result is checked against the
     source: stream durations, video packet count, subtitle count and a decode
     of the last 30 seconds. Any mismatch fails the script and leaves the
     output as <name>.mp4.partial for inspection; a good file is renamed into
     place only after every check passes.
   * Paths are passed with the call operator (&), never Start-Process
     -ArgumentList, which splits paths with spaces.

.EXAMPLE
  .\convert-video.ps1 "C:\Users\Lenovo\Documents\videos\Movie.mkv"

.EXAMPLE
  .\convert-video.ps1 Movie.mkv -VerifyOnly -Output "Movie (1080p).mp4"
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory, Position = 0)][string]$Source,
    # Default: "<source name> (<height>p).mp4" beside the source.
    [string]$Output,
    [int]$Height = 1080,
    # Lower is better: QSV global_quality / x264 CRF.
    [int]$Quality = 20,
    [ValidateSet('auto', 'qsv', 'x264')][string]$Encoder = 'auto',
    # Absolute ffprobe stream index of the audio to use. Default: the file's
    # default audio track, else the first.
    [int]$AudioStream = -1,
    # Skip converting; only run the checks on an existing -Output.
    [switch]$VerifyOnly,
    [switch]$Force
)

# Native-command stderr (ffmpeg's progress) must not become terminating errors
# on Windows PowerShell 5.1; every native call's $LASTEXITCODE is checked.
$ErrorActionPreference = 'Continue'

$TextSubtitleCodecs = @('subrip', 'srt', 'ass', 'ssa', 'webvtt', 'mov_text', 'text')
$DurationToleranceSeconds = 2.0

function Fail([string]$Message) { throw $Message }

function Get-Probe([string]$Path, [string]$Format) {
    $fmtArgs = @(); if ($Format) { $fmtArgs = @('-f', $Format) }
    $json = & ffprobe -v error @fmtArgs -show_streams -show_format -of json $Path
    if ($LASTEXITCODE -ne 0) { Fail "ffprobe could not read '$Path'" }
    return ($json -join "`n") | ConvertFrom-Json
}

function Get-PacketCount([string]$Path, [string]$Format) {
    $fmtArgs = @(); if ($Format) { $fmtArgs = @('-f', $Format) }
    $n = & ffprobe -v error @fmtArgs -select_streams v:0 -count_packets -show_entries stream=nb_read_packets -of csv=p=0 $Path
    return [int]($n | Select-Object -First 1)
}

function Get-StreamDuration($Stream, $Probe) {
    if ($Stream.duration -and $Stream.duration -ne 'N/A') { return [double]$Stream.duration }
    return [double]$Probe.format.duration
}

# Returns a list of problems; empty means the output matches the source.
function Test-Output([string]$SourcePath, [string]$OutPath, [string]$OutFormat) {
    $problems = New-Object System.Collections.Generic.List[string]
    $src = Get-Probe $SourcePath $null
    $out = Get-Probe $OutPath $OutFormat
    $srcDur = [double]$src.format.duration

    $v = @($out.streams | Where-Object codec_type -eq 'video')
    $a = @($out.streams | Where-Object codec_type -eq 'audio')
    $s = @($out.streams | Where-Object codec_type -eq 'subtitle')
    if ($v.Count -ne 1) { $problems.Add("expected 1 video stream, found $($v.Count)") }
    if ($a.Count -ne 1) { $problems.Add("expected 1 audio stream, found $($a.Count)") }

    $expectedSubs = @($src.streams | Where-Object { $_.codec_type -eq 'subtitle' -and $TextSubtitleCodecs -contains $_.codec_name }).Count
    if ($s.Count -ne $expectedSubs) { $problems.Add("expected $expectedSubs subtitle tracks, found $($s.Count)") }

    foreach ($pair in @(@('video', $v), @('audio', $a))) {
        if ($pair[1].Count -ge 1) {
            $d = Get-StreamDuration $pair[1][0] $out
            if ([math]::Abs($d - $srcDur) -gt $DurationToleranceSeconds) {
                $problems.Add(('{0} duration {1:n1}s differs from source {2:n1}s' -f $pair[0], $d, $srcDur))
            }
        }
    }

    $srcPackets = Get-PacketCount $SourcePath $null
    $outPackets = Get-PacketCount $OutPath $OutFormat
    if ([math]::Abs($srcPackets - $outPackets) -gt 2) {
        $problems.Add("video frame count $outPackets differs from source $srcPackets")
    }

    # The last 30s must decode cleanly (catches truncated/corrupt tails).
    $fmtArgs = @(); if ($OutFormat) { $fmtArgs = @('-f', $OutFormat) }
    $start = [math]::Max(0, [double]$out.format.duration - 30)
    $tail = & ffmpeg -v error -ss $start @fmtArgs -i $OutPath -map 0:v:0 -map 0:a:0 -f null - 2>&1
    if ($LASTEXITCODE -ne 0 -or ($tail | Where-Object { $_ })) {
        $problems.Add("decoding the last 30 seconds reported errors: $(($tail | Select-Object -First 3) -join ' | ')")
    }
    return $problems
}

# ---------------------------------------------------------------------------

foreach ($tool in 'ffmpeg', 'ffprobe') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { Fail "$tool not found on PATH" }
}
if (-not (Test-Path -LiteralPath $Source)) { Fail "Source not found: $Source" }
$Source = (Resolve-Path -LiteralPath $Source).Path

$srcProbe = Get-Probe $Source $null
$video = @($srcProbe.streams | Where-Object codec_type -eq 'video') | Select-Object -First 1
if (-not $video) { Fail 'Source has no video stream' }

$targetH = [math]::Min($Height, [int]$video.height)
$targetH -= $targetH % 2
$targetW = [int][math]::Round([int]$video.width * $targetH / [int]$video.height)
$targetW -= $targetW % 2

if (-not $Output) {
    $dir = Split-Path -Parent $Source
    $Output = Join-Path $dir ('{0} ({1}p).mp4' -f [IO.Path]::GetFileNameWithoutExtension($Source), $targetH)
}
$Output = [IO.Path]::GetFullPath($Output)
$Partial = "$Output.partial"

if ($VerifyOnly) {
    if (-not (Test-Path -LiteralPath $Output)) { Fail "Output not found: $Output" }
    $problems = Test-Output $Source $Output $null
    if ($problems.Count -gt 0) { $problems | ForEach-Object { Write-Host "FAIL: $_" -ForegroundColor Red }; exit 1 }
    Write-Host 'OK: output matches the source.' -ForegroundColor Green
    exit 0
}

if ((Test-Path -LiteralPath $Output) -and -not $Force) { Fail "Output already exists (use -Force to replace): $Output" }

# --- choose streams ---------------------------------------------------------
$audioStreams = @($srcProbe.streams | Where-Object codec_type -eq 'audio')
if ($audioStreams.Count -eq 0) { Fail 'Source has no audio stream' }
if ($AudioStream -ge 0) {
    $audio = $audioStreams | Where-Object { [int]$_.index -eq $AudioStream } | Select-Object -First 1
    if (-not $audio) { Fail "No audio stream with index $AudioStream" }
} else {
    $audio = $audioStreams | Where-Object { $_.disposition.default -eq 1 } | Select-Object -First 1
    if (-not $audio) { $audio = $audioStreams[0] }
}
$subs = @($srcProbe.streams | Where-Object { $_.codec_type -eq 'subtitle' -and $TextSubtitleCodecs -contains $_.codec_name })
$skipped = @($srcProbe.streams | Where-Object { $_.codec_type -eq 'subtitle' -and $TextSubtitleCodecs -notcontains $_.codec_name })
foreach ($k in $skipped) { Write-Warning "Skipping image-based subtitle stream #$($k.index) ($($k.codec_name)); it cannot be converted to text." }
if ($video.color_transfer -in @('smpte2084', 'arib-std-b67')) {
    Write-Warning 'Source is HDR; this script does not tone-map, so colours will look washed out. Convert with a tone-mapping filter instead.'
}
$lang = if ($audio.tags -and $audio.tags.language) { $audio.tags.language } else { 'und' }

# --- choose encoder ---------------------------------------------------------
function Test-Qsv {
    $null = & ffmpeg -v error -f lavfi -i color=c=black:s=320x240:d=0.2 -vf format=nv12 -c:v h264_qsv -f null - 2>&1
    return ($LASTEXITCODE -eq 0)
}
$useQsv = switch ($Encoder) { 'qsv' { $true } 'x264' { $false } default { Test-Qsv } }
Write-Host ("Source : {0} ({1}x{2} {3}, audio #{4} {5})" -f $Source, $video.width, $video.height, $video.codec_name, $audio.index, $lang)
Write-Host ("Output : {0} ({1}x{2}, {3}, {4} subtitle tracks)" -f $Output, $targetW, $targetH, $(if ($useQsv) { 'Intel QSV' } else { 'x264' }), $subs.Count)

$hwDecodable = $useQsv -and ($video.codec_name -in @('h264', 'hevc')) -and ($video.pix_fmt -in @('yuv420p', 'yuv420p10le'))
if ($hwDecodable) {
    $inArgs = @('-hwaccel', 'qsv', '-hwaccel_output_format', 'qsv', '-c:v', "$($video.codec_name)_qsv")
    $vf = "vpp_qsv=w=${targetW}:h=${targetH}:format=nv12"
} elseif ($useQsv) {
    $inArgs = @()
    $vf = "scale=${targetW}:${targetH}:flags=lanczos,format=nv12"
} else {
    $inArgs = @()
    $vf = "scale=${targetW}:${targetH}:flags=lanczos,format=yuv420p"
}
if ($useQsv) { $vEnc = @('-c:v', 'h264_qsv', '-profile:v', 'high', '-global_quality', "$Quality", '-preset', 'slow') }
else { $vEnc = @('-c:v', 'libx264', '-profile:v', 'high', '-preset', 'medium', '-crf', "$Quality") }

$tmpVideo = "$Output.video.tmp"
$tmpAudio = "$Output.audio.tmp"
try {
    # Pass 1: video + text subtitles, no audio.
    $maps = @('-map', '0:v:0'); foreach ($sub in $subs) { $maps += @('-map', "0:$($sub.index)") }
    Write-Host '[1/3] Encoding video + subtitles...'
    & ffmpeg -hide_banner -v warning -stats -stats_period 15 -y @inArgs -i $Source @maps -map_metadata 0 -an -vf $vf @vEnc -c:s mov_text -f mp4 $tmpVideo
    if ($LASTEXITCODE -ne 0) { Fail "video pass failed (ffmpeg exit $LASTEXITCODE)" }

    # Pass 2: audio only (separate on purpose, see the header).
    Write-Host '[2/3] Encoding audio...'
    & ffmpeg -hide_banner -v warning -stats -stats_period 15 -y -i $Source -vn -map "0:$($audio.index)" -c:a aac -b:a 192k -ac 2 -f mp4 $tmpAudio
    if ($LASTEXITCODE -ne 0) { Fail "audio pass failed (ffmpeg exit $LASTEXITCODE)" }

    # Pass 3: stream-copy mux, faststart.
    Write-Host '[3/3] Muxing...'
    & ffmpeg -hide_banner -v error -y -i $tmpVideo -i $tmpAudio -map 0 -map 1:a:0 -c copy "-metadata:s:a:0" "language=$lang" -movflags +faststart -f mp4 $Partial
    if ($LASTEXITCODE -ne 0) { Fail "mux failed (ffmpeg exit $LASTEXITCODE)" }
} finally {
    Remove-Item -LiteralPath $tmpVideo, $tmpAudio -ErrorAction SilentlyContinue
}

Write-Host 'Verifying against the source...'
$problems = Test-Output $Source $Partial 'mp4'
if ($problems.Count -gt 0) {
    $problems | ForEach-Object { Write-Host "FAIL: $_" -ForegroundColor Red }
    Write-Host "Kept for inspection: $Partial" -ForegroundColor Yellow
    exit 1
}
Move-Item -LiteralPath $Partial -Destination $Output -Force
Write-Host ('OK: {0} ({1:n1} GB). The original was not touched.' -f $Output, ((Get-Item -LiteralPath $Output).Length / 1GB)) -ForegroundColor Green
