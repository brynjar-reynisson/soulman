# One-time setup: pre-creates Windows Firewall inbound-allow rules for every
# Soulman service executable (dev + prod) so Windows never has to prompt for
# them again after a rebuild+restart.
#
# Why this works: each run-<svc>.ps1 does `go build -o $exe .`, which
# overwrites the binary in place at the same fixed path every time. Windows
# Firewall's per-program rules are keyed on that path, not on the file's
# hash/signature, so a rule created once here keeps matching after every
# future rebuild - that's what makes the exe "changed and restarted" prompt
# go away for good instead of reappearing on each restart.
#
# Must be run from an elevated (Administrator) PowerShell:
#   Right-click PowerShell -> "Run as Administrator", then:
#   .\setup-firewall-rules.ps1

#Requires -RunAsAdministrator

$ErrorActionPreference = "Stop"

$services = @("perception-svc", "memory-svc", "thinking-svc", "action-svc", "web-svc")

$envs = @(
    @{ Label = "dev";  Root = "C:\Users\Lenovo\soulman-dev" },
    @{ Label = "prod"; Root = "C:\Users\Lenovo\soulman-prod" }
)

foreach ($svcEnv in $envs) {
    foreach ($svc in $services) {
        $exe = Join-Path $svcEnv.Root "bin\$svc.exe"
        $displayName = "Soulman $svc ($($svcEnv.Label))"

        # Idempotent: drop any prior rule with this name before recreating it,
        # so re-running this script never produces duplicates.
        Remove-NetFirewallRule -DisplayName $displayName -ErrorAction SilentlyContinue | Out-Null

        New-NetFirewallRule `
            -DisplayName $displayName `
            -Direction Inbound `
            -Program $exe `
            -Action Allow `
            -Protocol TCP `
            -Profile Private,Domain `
            | Out-Null

        if (-not (Test-Path $exe)) {
            Write-Warning "${displayName}: rule created for $exe, but that file doesn't exist yet (never built). The rule will still apply once it's built."
        } else {
            Write-Output "Allowed: $displayName -> $exe"
        }
    }
}

Write-Output ""
Write-Output "Done. Rules apply on Private/Domain network profiles only (not Public)."
Write-Output "Re-run this script any time an exe's path changes (e.g. bin dir moves)."
