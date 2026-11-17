# Отдельные SSH-выпуски ChatGPT и Codex

Старые меню «Обновления», ветка packages, публичный ключ Ed25519, код проверки подписи и действующий подписанный релиз не изменяются.

## Как работают новые выпуски

GitHub Actions в файле .github/workflows/publish-ssh.yml выполняет scripts/check.sh, собирает пять архитектур, затем scripts/publish-ssh-release.py создаёт отдельную ветку ssh-releases с неизменяемыми каталогами v<VERSION>. При повторном выпуске той же версии публикация отказывается перезаписывать архивы. Старая ветка packages не затрагивается.

В каждой версии лежат release-info.json (версия, коммит, SHA-256, источник ChatGPT или Codex), SOURCE.txt, SHA256SUMS, пять архивов и установщик install-chatgpt.sh либо install-codex.sh с зашитым SHA-256 манифеста.

Чтобы выпустить новую сборку Codex, надо изменить VERSION, указать Codex в release-source.txt и запустить новый workflow. Версия ChatGPT помечается ChatGPT. Подписи старого updater для SSH-канала не используются.

## Установка из Windows

Подключитесь по SSH к Entware как root. Скачайте установщик версии из ветки ssh-releases, сверяйте его SHA-256 с заранее известным значением из release-info.json, только потом запустите sh. Скрипт повторно проверяет SHA-256 SHA256SUMS и архива нужной архитектуры и вызывает старый проверенный штатный установщик с --skip-packages. Конфигурация, пароль и история Pilot сохраняются; при ошибке действует его прежний откат. Служба кратко перезапустится.

Это ручной SSH-канал, защищённый HTTPS GitHub и контрольными суммами. Он не равноценен автоматическому каналу с отдельной Ed25519-подписью. Сам механизм панели оставлен в прежнем виде.

## Готовый выпуск: 0.2.0-alpha.18 [ChatGPT]

Публикация подтверждена: [GitHub Actions run #44039732572](https://github.com/punisher89/my.kn-test-/actions/runs/44039732572).
Контрольная сумма установщика SHA-256 — из опубликованного release-info.json:
7cb553cc862d13744909e83bbd422ef4a8bebd9bddd7e2664728ebf5cd7738a2.

Выполните на самом роутере под root через SSH (PuTTY на Windows подходит):

```sh
f=$(mktemp /opt/.pilot-chatgpt.XXXXXX) || exit 1
(
  trap 'rm -f "$f"' EXIT
  curl --proto '=https' --tlsv1.2 -fSL 'https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/ssh-releases/v0.2.0-alpha.18/install-chatgpt.sh' -o "$f" &&
  printf '%s  %s\n' '7cb553cc862d13744909e83bbd422ef4a8bebd9bddd7e2664728ebf5cd7738a2' "$f" | sha256sum -c - &&
  sh "$f"
)
```

После успеха бинарник показывает 0.2.0-alpha.18 и панель входит со старым
паролем. Для отката оставлена предыдущая копия программы. До первых тестов
на KN-1811 не объявляем фактический обход YouTube и Telegram подтверждённым.

Проверено, что старая signed-подписанная ветка packages/latest.json осталась
на 0.2.0-alpha.17 — её механизм не изменён.
