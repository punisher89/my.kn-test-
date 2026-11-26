#!/usr/bin/env bash
set -euo pipefail
umask 077
cd -- "$(dirname -- "$0")"
fail(){ printf 'Ошибка: %s\n' "$*" >&2; exit 1; }
[[ $(id -u) == 0 ]] || fail 'Запусти через sudo bash install.sh'
[[ -t 0 ]] || fail 'Запусти из интерактивного SSH-терминала'
for x in python3 systemctl install; do command -v "$x" >/dev/null || fail "Не найден $x"; done
python3 -c 'import sys;sys.exit(sys.version_info < (3,9))' || fail 'Требуется Python 3.9 или новее'
[[ -d /run/systemd/system ]] || fail 'На VPS должен работать systemd'
[[ -f bot.py && -f pilot-bot.service ]] || fail 'Скачай всю папку telegram-bot'
systemd_ver=$(systemctl --version | awk 'NR==1 {print $2}')
[[ $systemd_ver =~ ^[0-9]+$ ]] || fail 'Не удалось определить версию systemd'
(( systemd_ver >= 235 )) || fail 'Требуется systemd 235 или новее'
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
conf=/etc/pilot-bot
if [[ -s "$conf/bot-token" ]]; then
    install -m 0600 "$conf/bot-token" "$tmp/token"
    echo 'Существующий токен сохранён.'
else
    read -rsp 'Токен бота от BotFather (ввод скрыт): ' token
    echo
    [[ $token =~ ^[0-9]+:[A-Za-z0-9_-]+$ ]] || fail 'Неверный формат токена'
    printf '%s\n' "$token" > "$tmp/token"
    unset token
fi
if [[ -s "$conf/config.env" ]]; then
    install -m 0600 "$conf/config.env" "$tmp/config"
    echo 'Существующие настройки сохранены.'
else
    read -rp 'Твой числовой Telegram ID: ' owner
    [[ $owner =~ ^[1-9][0-9]*$ ]] || fail 'Нужен числовой ID, не @username'
    read -rp 'ID группы (отрицательный, Enter — без группы): ' group
    group=${group:-0}
    [[ $group =~ ^(0|-[1-9][0-9]*)$ ]] || fail 'Неверный ID группы'
    printf 'PILOT_OWNER_IDS=%s\nPILOT_GROUP_ID=%s\n' "$owner" "$group" > "$tmp/config"
fi
python3 - "$tmp/token" "$tmp/config" <<'PY'
import json, re, sys, urllib.request, urllib.error
from pathlib import Path
token = Path(sys.argv[1]).read_text().strip()
if not re.fullmatch(r"[0-9]+:[A-Za-z0-9_-]+",token):
    sys.exit("Ошибка: неверный формат сохранённого токена")
cfg = {}
for line in Path(sys.argv[2]).read_text().splitlines():
    line = line.strip()
    if line and not line.startswith("#") and "=" in line:
        k,v=line.split("=",1)
        cfg[k.strip()]=v.strip().strip("'\"")
try:
    assert all(int(v)>0 for v in cfg["PILOT_OWNER_IDS"].split(","))
    assert int(cfg.get("PILOT_GROUP_ID","0")) <= 0
except (AssertionError, KeyError, ValueError):
    sys.exit("Ошибка: проверь PILOT_OWNER_IDS и PILOT_GROUP_ID в config.env")
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs):
        return None
opener=urllib.request.build_opener(NoRedirect(),urllib.request.ProxyHandler({}))
try:
    with opener.open("https://api.telegram.org/bot"+token+"/getMe",timeout=15) as r:
        data=json.load(r)
    if not data.get("ok"):
        sys.exit("Ошибка: Telegram не подтвердил токен")
    print("Telegram подтвердил токен: @"+data["result"]["username"])
except urllib.error.HTTPError as e:
    sys.exit("Ошибка: Telegram HTTP "+str(e.code)+"; проверь токен и доступность API")
except Exception:
    sys.exit("Ошибка: VPS не смог подключиться к Telegram API; проверь сеть, DNS и TLS")
PY
install -d -m 0755 /opt/pilot-bot
install -d -m 0700 "$conf"
install -m 0644 bot.py /opt/pilot-bot/bot.py
install -m 0600 "$tmp/config" "$conf/config.env"
install -m 0600 "$tmp/token" "$conf/bot-token"
if (( systemd_ver >= 247 )); then
    install -m 0644 pilot-bot.service /etc/systemd/system/pilot-bot.service
else
    echo 'Старый systemd: включаю совместимый режим без LoadCredential.'
    command -v useradd >/dev/null || fail 'Не найден useradd'
    if ! id pilot-bot >/dev/null 2>&1; then
        useradd --system --user-group --home-dir /var/lib/pilot-bot --no-create-home --shell /usr/sbin/nologin pilot-bot
    fi
    chown root:pilot-bot "$conf" "$conf/bot-token"
    chmod 0750 "$conf"
    chmod 0640 "$conf/bot-token"
    install -m 0644 pilot-bot-legacy.service /etc/systemd/system/pilot-bot.service
fi
systemctl daemon-reload
systemctl enable pilot-bot.service
systemctl restart pilot-bot.service
sleep 3
if ! systemctl is-active --quiet pilot-bot.service; then
    echo 'Служба не запустилась. Диагностика:'
    systemctl status pilot-bot.service --no-pager -l || true
    exit 1
fi
echo 'Служба запущена. Открой своего бота в Telegram и отправь /start.'
echo 'Логи: journalctl -u pilot-bot -n 50 --no-pager'
