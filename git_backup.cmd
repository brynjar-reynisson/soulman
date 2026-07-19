powershell -Command "git archive --format=zip HEAD -o ../brynjar-obsidian_$([DateTime]::Now.ToString('yyyy-MM-dd')).zip"

:: Create a new directory for the backup
:: mkdir "../brynjar-obsidian-restored"

:: Extract the zip into the new directory
:: tar -xf "../brynjar-obsidian_2026-06-23.zip" -C "../brynjar-obsidian-restored"