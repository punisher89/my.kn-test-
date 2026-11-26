# Keenetic Pilot Telegram Companion 0.1

Python 3.9+, без pip. Русское меню, состояние панели, проверка публикаций ChatGPT, уведомления в группе. Управление только через отдельный ограниченный API /api/bot/* (не входит в beta.6). Не заменяет Telegram-туннель.

## Быстрая установка на VPS (Debian/Ubuntu)

```sh
sudo apt-get update
sudo apt-get install -y git python3 ca-certificates
git clone --single-branch --branch feature/keenetic-pilot-telegram-bot https://github.com/punisher89/my.kn-test-.git pilot-companion
cd pilot-companion/telegram-bot
sudo bash install.sh
```

Установщик спросит токен BotFather, числовой Telegram ID владельца и ID группы (необязательно), проверит доступ к Telegram API и запустит службу. Существующие токен и настройки сохраняются при повторном запуске. Нужны Python 3.9+ и systemd 235+. Для systemd до 247 предусмотрена отдельная служба без LoadCredential; дополнительные ключи на таких системах настраиваются через защищённые файлы, доступные пользователю pilot-bot.

При ошибке: `sudo journalctl -u pilot-bot -n 50 --no-pager`. Не публикуйте токены.

## Ручная установка на Linux VPS с systemd 247+

Создайте бота через BotFather. Получите числовой ID владельца и группы, добавьте бота в группу с правом отправлять сообщения. Скопируйте bot.py и pilot-bot.service на VPS.

```sh
install -d -m 0755 /opt/pilot-bot
install -d -m 0700 /etc/pilot-bot
install -m 0644 bot.py /opt/pilot-bot/bot.py
install -m 0644 pilot-bot.service /etc/systemd/system/pilot-bot.service
umask 077
touch /etc/pilot-bot/bot-token /etc/pilot-bot/config.env
chmod 600 /etc/pilot-bot/bot-token /etc/pilot-bot/config.env
```

В редакторе заполните bot-token одним токеном, config.env — PILOT_OWNER_IDS=числовой_ID и PILOT_GROUP_ID=отрицательный_ID (0 отключает объявления). Для приватного GitHub можно подключить read-only Contents token через systemd drop-in LoadCredential=github-token:/etc/pilot-bot/github-token и Environment=PILOT_GITHUB_TOKEN_FILE=%d/github-token.

Настройте защищённый канал до роутера, например существующий SSH local forward на 127.0.0.1:19080 на VPS, с проверенным host key. Не открывайте порт панели в интернет. Проверка curl http://127.0.0.1:19080/healthz должна вернуть JSON. Без канала объявления всё равно работают.

```sh
systemctl daemon-reload
systemctl enable --now pilot-bot
journalctl -u pilot-bot -n 30 --no-pager
```

Для управления потребуется новая версия Pilot с отдельным ключом /api/bot/*; beta.6 поддерживает только /healthz. Не используйте основной административный токен. Для ограниченного ключа — systemd drop-in LoadCredential=control-token:/etc/pilot-bot/control-token и Environment=PILOT_CONTROL_TOKEN_FILE=%d/control-token. Перезапустите службу после изменения. Все команды подтверждаются в личном чате, окно 60 секунд. Группа получает только публичную информацию о релизах.

Тесты: python3 -m unittest discover -s telegram-bot. Реальный Telegram и VPS требуют проверки с вашими учетными данными. Не отправляйте токены в чат.
