#!/usr/bin/env python3
"""VPS companion, Python 3.9+, standard library only."""
import html
import json
import logging
import os
import re
import secrets
import time
import urllib.parse
import urllib.request
from pathlib import Path

BASE = "https://raw.githubusercontent.com/punisher89/my.kn-test-/refs/heads/ssh-releases/"
VERSION = re.compile(r"0\.\d+\.\d+-beta\.\d+\Z")
LOG = logging.getLogger("pilot-bot")

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None

OPENER = urllib.request.build_opener(NoRedirect(), urllib.request.ProxyHandler({}))

def request(url, body=None, headers=None, timeout=15):
    h = dict(headers or {})
    if body is not None:
        h["Content-Type"] = "application/json"
    req = urllib.request.Request(url, None if body is None else json.dumps(body).encode(), h)
    with OPENER.open(req, timeout=timeout) as response:
        raw = response.read(512001)
        if len(raw) > 512000:
            raise ValueError("oversized response")
        return json.loads(raw)

def secret(env, optional=False):
    path = os.environ.get(env)
    if not path and optional:
        return ""
    value = Path(path).read_text().strip()
    if not value or any(c.isspace() for c in value):
        raise ValueError("invalid secret")
    return value

def esc(v):
    return html.escape(str(v)[:180])

def valid_release(d):
    return (isinstance(d, dict) and d.get("kind") == "pilot-ssh-release"
            and d.get("channel") == "ssh" and d.get("source") == "ChatGPT"
            and d.get("installed_via") == "ssh"
            and d.get("signed_panel_updater_modified") is False
            and bool(VERSION.fullmatch(str(d.get("version", ""))))
            and bool(re.fullmatch(r"[0-9a-f]{40}", str(d.get("git_sha", ""))))
            and all(re.fullmatch(r"[0-9a-f]{64}", str(d.get(k, "")))
                    for k in ("manifest_sha256", "installer_sha256", "engine_sha256")))

def release(fetch):
    d = fetch(BASE + "latest-chatgpt-beta.json")
    if not valid_release(d) or fetch(BASE + "v" + d["version"] + "/release-info.json") != d:
        raise ValueError("release pointer mismatch")
    return d

def keyboard(control=False):
    rows = [[{"text": "📡 Состояние", "callback_data": "status"},
             {"text": "📦 Обновления", "callback_data": "release"}]]
    if control:
        rows += [[{"text": "▶ Включить", "callback_data": "ask:on"},
                  {"text": "⏸ Отключить", "callback_data": "ask:off"}],
                 [{"text": "🧪 Проверить YouTube", "callback_data": "ask:youtube"},
                  {"text": "🔧 Подбор YouTube", "callback_data": "ask:tune"}],
                 [{"text": "🛑 Отменить подбор", "callback_data": "ask:cancel"}]]
    rows += [[{"text": "🔄 Главное меню", "callback_data": "home"},
              {"text": "ℹ️ Помощь", "callback_data": "help"}]]
    return {"inline_keyboard": rows}

def release_text(d):
    return ("🚀 <b>KEENETIC PILOT · CHATGPT</b>\n"
            "━━━━━━━━━━━━━━━━━━━━\n"
            f"📦 Версия: <code>{esc(d['version'])}</code>\n"
            f"🧬 Движок: <code>{esc(d.get('engine_version','—'))}</code>\n"
            f"🕓 Опубликовано: {esc(d.get('published_at','—'))}\n"
            "🔎 Индекс совпадает с метаданными релиза.\n\n"
            "Установка — в панели Pilot, канал ChatGPT.")

def private_allowed(message, user, owners):
    chat = message.get("chat") or {}
    return chat.get("type") == "private" and user.get("id") in owners and chat.get("id") == user.get("id")

ACTIONS = {"on": "Включить обход", "off": "Отключить обход", "youtube": "Проверить YouTube",
           "tune": "Запустить подбор YouTube", "cancel": "Отменить подбор"}

class Bot:
    def __init__(self, token, owners, group, base, state, github_token="", control_token=""):
        self.token, self.owners, self.group = token, owners, group
        self.base, self.state_path = base, Path(state)
        self.github_token, self.control_token = github_token, control_token
        self.state = {"offset": 0, "announced": ""}
        if self.state_path.exists():
            data = json.loads(self.state_path.read_text())
            self.state.update({k: data[k] for k in self.state if k in data})
        self.rates, self.pending = {}, {}
    def save(self):
        tmp = self.state_path.with_suffix(".tmp")
        with os.fdopen(os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
            json.dump(self.state, f)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, self.state_path)
    def telegram(self, method, data, timeout=15):
        r = request("https://api.telegram.org/bot" + self.token + "/" + method, data, timeout=timeout)
        if not r.get("ok"):
            raise ValueError("Telegram request failed")
        return r["result"]
    def send(self, chat, text, menu=False, markup=None):
        p = {"chat_id": chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": True}
        if markup is not None:
            p["reply_markup"] = markup
        elif menu:
            p["reply_markup"] = keyboard(bool(self.control_token))
        return self.telegram("sendMessage", p)
    def fetch_release(self):
        headers = {"Cache-Control": "no-cache"}
        if self.github_token:
            headers["Authorization"] = "token " + self.github_token
        return release(lambda u: request(u, headers=headers))
    def announce(self):
        if not self.group:
            return
        d = self.fetch_release()
        previous, version = self.state["announced"], d["version"]
        def key(v):
            return tuple(map(int, re.findall(r"\d+", v)))
        if previous and VERSION.fullmatch(previous) and key(version) <= key(previous):
            return
        self.send(self.group, release_text(d))
        self.state["announced"] = version
        self.save()
    def pilot(self, path, body=None, timeout=8):
        return request(self.base + path, body, {"Authorization":"Bearer " + self.control_token}, timeout)
    def status(self):
        try:
            d = request(self.base + "/healthz", timeout=5)
            if d.get("status") != "ok":
                raise ValueError("health")
            out = ("📡 <b>Состояние Pilot</b>\n━━━━━━━━━━━━━━━━━━━━\n"
                   "🟢 Панель отвечает\n"
                   f"📦 Версия: <code>{esc(d.get('version','—'))}</code>\n"
                   f"⚙️ Режим: {esc(d.get('mode','—'))}\n")
            if self.control_token:
                try:
                    s = self.pilot("/api/bot/status")
                    out += ("🛡 Обход: " + ("работает" if s.get("running") else "остановлен") +
                            "\n🔄 Занят: " + ("да" if s.get("busy") else "нет") +
                            f"\n📋 Очередь: {esc(s.get('domain_queue','—'))}\n")
                except Exception:
                    out += "⚠️ Ограниченный API управления недоступен.\n"
            return out + "\nДоступность панели не подтверждает работу всех сервисов."
        except Exception:
            return "🔴 Панель Pilot недоступна. Проверь приватное подключение VPS."
    def content(self, action):
        if action == "status":
            return self.status()
        if action == "release":
            try:
                return release_text(self.fetch_release())
            except Exception:
                return "⚠️ Не удалось проверить публикацию. Повтори позже."
        if action == "help":
            return ("ℹ️ <b>Помощь</b>\nУведомления — в группе, меню — только владельцу в личном чате.\n"
                    "Управление требует отдельного ключа API и защищённого канала VPS → роутер.")
        return ("🟩 <b>KEENETIC PILOT</b>\n━━━━━━━━━━━━━━━━━━━━\n"
                "◈ Центр управления · VPS Companion\n\n"
                + self.status() + "\n\nВыбери раздел ниже.")
    def handle(self, update):
        cb = update.get("callback_query")
        msg = (cb.get("message") or {}) if cb else (update.get("message") or {})
        user = (cb.get("from") or {}) if cb else (msg.get("from") or {})
        if not private_allowed(msg, user, self.owners):
            if cb:
                self.telegram("answerCallbackQuery", {"callback_query_id": cb["id"], "text": "Доступ закрыт."})
            return
        uid = user["id"]
        now = time.monotonic()
        if now - self.rates.get(uid, -100) < 1:
            if cb:
                self.telegram("answerCallbackQuery", {"callback_query_id": cb["id"], "text": "Подожди секунду."})
            return
        self.rates[uid] = now
        if cb:
            action = cb.get("data","")
            self.telegram("answerCallbackQuery", {"callback_query_id": cb["id"]})
            if action in ("status", "release", "home", "help"):
                self.send(uid, self.content(action), True)
            elif self.control_token and action.startswith("ask:") and action[4:] in ACTIONS:
                key = secrets.token_urlsafe(12)
                name = action[4:]
                self.pending[uid] = (key, name, now + 60)
                self.send(uid, "⚠️ <b>" + ACTIONS[name] + "?</b>\nПодтверждение действует 60 секунд.",
                          markup={"inline_keyboard":[[{"text":"✅ Подтвердить","callback_data":"confirm:"+key},
                                                       {"text":"↩ Отмена","callback_data":"dismiss"}]]})
            elif action == "dismiss":
                self.pending.pop(uid, None)
                self.send(uid, "Отменено.", True)
            elif self.control_token and action.startswith("confirm:"):
                item = self.pending.get(uid)
                if not item or item[0] != action[8:] or now > item[2]:
                    self.send(uid, "Подтверждение истекло или недействительно.", True)
                    return
                self.pending.pop(uid, None)
                try:
                    result = self.pilot("/api/bot/action", {"action":item[1]}, 45)
                    self.send(uid, "✅ Команда принята: " + ACTIONS[item[1]] +
                              "\n" + esc(result.get("status","")) + "\nПроверь состояние после выполнения.", True)
                except Exception:
                    self.send(uid, "⚠️ Ответ не получен или команда отклонена. Проверь состояние; не повторяй автоматически.", True)
        elif msg.get("text") in ("/start", "/menu", "/status", "/updates"):
            action = {"/status":"status", "/updates":"release"}.get(msg["text"], "home")
            self.send(uid, self.content(action), True)
    def run(self):
        delay, next_release = 2, 0
        while True:
            try:
                if time.monotonic() >= next_release:
                    next_release = time.monotonic() + 120
                    try:
                        self.announce()
                    except Exception as e:
                        LOG.warning("release check failed: %s", type(e).__name__)
                updates = self.telegram("getUpdates", {"offset": self.state["offset"], "timeout": 25,
                    "allowed_updates": ["message", "callback_query"]}, timeout=35)
                for u in updates:
                    if u["update_id"] < self.state["offset"]:
                        continue
                    self.state["offset"] = u["update_id"] + 1
                    self.save()
                    try:
                        self.handle(u)
                    except Exception as e:
                        LOG.warning("update failed: %s", type(e).__name__)
                delay = 2
            except Exception as e:
                LOG.warning("poll failed: %s", type(e).__name__)
                time.sleep(delay)
                delay = min(delay * 2, 60)

def main():
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    owners = {int(s) for s in os.environ["PILOT_OWNER_IDS"].split(",")}
    if not owners or min(owners) <= 0:
        raise ValueError("positive owner IDs required")
    group = int(os.environ.get("PILOT_GROUP_ID", "0"))
    if group > 0:
        raise ValueError("group ID must be negative")
    base = os.environ.get("PILOT_BASE_URL", "http://127.0.0.1:19080").rstrip("/")
    u = urllib.parse.urlsplit(base)
    if u.scheme != "http" or u.hostname not in ("127.0.0.1", "::1") or u.username or u.password or u.path or u.query or u.fragment:
        raise ValueError("Pilot base URL must be loopback HTTP via private forwarding")
    Bot(secret("PILOT_BOT_TOKEN_FILE"), owners, group, base,
        os.environ.get("PILOT_STATE", "/var/lib/pilot-bot/state.json"),
        secret("PILOT_GITHUB_TOKEN_FILE", True), secret("PILOT_CONTROL_TOKEN_FILE", True)).run()

if __name__ == "__main__":
    main()
