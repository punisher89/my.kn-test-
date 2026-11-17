# Windows PowerShell 5.1+ / PowerShell 7.
# Safely connects the ORIGINAL Ed25519 signing PEM to this repository's
# GitHub Actions secret and reruns the release of the EXACT branch head.
# Never commits, prints, or sends the PEM to ChatGPT.
[CmdletBinding()]
param(
    [string]$KeyPath = ""
)

$ErrorActionPreference = "Stop"
$repo = "punisher89/my.kn-test-"
$branch = "feature/youtube-video-range-check"
$workflow = "publish-alpha18.yml"
$secret = "PILOT_UPDATES_SIGNING_PEM"

function Fail([string]$message) { throw $message }
Write-Host "Keenetic Pilot alpha.18 - Windows signed release" -ForegroundColor Cyan

if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    Fail "GitHub CLI gh не найден. Установите через PowerShell: winget install --id GitHub.cli -e ; откройте новое окно PowerShell и повторите."
}
& gh auth status 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "Войдите в свой GitHub аккаунт:"
    & gh auth login
    if ($LASTEXITCODE -ne 0) { Fail "Не удалось войти через GitHub CLI." }
}
# Identify a run for precisely the latest commit: an old failed run would
# publish an outdated artifact under the same immutable alpha.18 version.
$head = (& gh api "repos/$repo/branches/feature%2Fyoutube-video-range-check" --jq ".commit.sha" | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $head -notmatch '^[0-9a-f]{40}$') { Fail "Не удалось проверить актуальную ветку." }

$runsText = (& gh run list --repo $repo --workflow $workflow --branch $branch --limit 20 --json databaseId,headSha,status,conclusion | Out-String)
if ($LASTEXITCODE -ne 0) { Fail "Не удалось получить список запусков релиза." }
$runs = @(ConvertFrom-Json $runsText)
$run = $runs | Where-Object { $_.headSha -eq $head } | Select-Object -First 1
if (-not $run) {
    Fail "Для последнего коммита нет запуска публикации. Откройте GitHub Actions и сообщите об этой ошибке; старую сборку alpha.18 запускать нельзя."
}

if ([string]::IsNullOrWhiteSpace($KeyPath)) {
    $candidates = @(
        (Join-Path (Get-Location).Path 'local\updates-signing.pem'),
        (Join-Path $PSScriptRoot '..\local\updates-signing.pem'),
        (Join-Path $env:USERPROFILE 'updates-signing.pem'),
        (Join-Path $env:USERPROFILE 'Documents\updates-signing.pem')
    )
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            $KeyPath = $candidate
            break
        }
    }
}
if ([string]::IsNullOrWhiteSpace($KeyPath)) {
    Write-Host "Исходный ключ не найден в стандартных папках." -ForegroundColor Yellow
    $KeyPath = Read-Host "Введите ПУТЬ к оригинальному updates-signing.pem (не содержимое ключа)"
}
if (-not (Test-Path -LiteralPath $KeyPath -PathType Leaf)) {
    Fail "Не найден файл. Не создавайте новый ключ: установленная панель ему не доверяет."
}
$KeyPath = (Resolve-Path -LiteralPath $KeyPath).Path
$keyText = [System.IO.File]::ReadAllText($KeyPath, [Text.Encoding]::ASCII)
if ($keyText -notmatch '-----BEGIN PRIVATE KEY-----' -or $keyText -notmatch '-----END PRIVATE KEY-----') {
    Fail "Файл не похож на Ed25519 PEM. Нужен именно оригинальный updates-signing.pem; публичный updates-public.key не подходит."
}
Write-Host "Отправляю ключ в защищённый GitHub Actions Secret. В терминале ключ не отображается."
try {
    # gh secret set reads stdin and encrypts the value locally before upload.
    $keyText | & gh secret set $secret --repo $repo
    if ($LASTEXITCODE -ne 0) { Fail "GitHub не принял секрет. Проверьте права на репозиторий." }
} finally {
    $keyText = $null
}

if ($run.status -ne 'completed') {
    Write-Host "Текущий release run ещё выполняется: https://github.com/$repo/actions/runs/$($run.databaseId)"
    Write-Host "Новый секрет применится при следующем запуске; если run завершится с ошибкой, нажмите Re-run failed jobs."
    exit 0
}
if ($run.conclusion -eq 'success') {
    Write-Host "Публикация уже прошла успешно: https://github.com/$repo/actions/runs/$($run.databaseId)"
    exit 0
}
if ($run.conclusion -ne 'failure') {
    Fail "Состояние релиза: $($run.conclusion). Повторный запуск автоматически не производится."
}

Write-Host "Повторяю подписание и публикацию release run #$($run.databaseId), SHA $($head.Substring(0,8))."
& gh run rerun $run.databaseId --failed --repo $repo
if ($LASTEXITCODE -ne 0) { Fail "Не удалось повторить GitHub Actions run." }
Write-Host "Откройте результат: https://github.com/$repo/actions/runs/$($run.databaseId)" -ForegroundColor Green
Write-Host "Когда будет SUCCESS, в панели: Обновления -> Проверить версию -> Установить alpha.18."
