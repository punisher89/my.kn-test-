"use strict";

const $ = (s) => document.querySelector(s);
const esc = (s) =>
  String(s ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const labels = {
  overview: "Обзор",
  profiles: "Сервисы и домены",
  tune: "Подбор стратегий",
  logs: "Журнал",
  settings: "Настройки",
};
let state,
  page = "overview",
  token = sessionStorage.getItem("pilot-token") || "",
  toastTimer;

async function api(path, method = "GET", data) {
  const response = await fetch(`/api/${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: data === undefined ? undefined : JSON.stringify(data),
  });
  const result = await response.json();
  if (!response.ok) {
    if (response.status === 401) logout();
    throw new Error(result.error || `HTTP ${response.status}`);
  }
  return result;
}
function notify(message, error = false) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.className = `toast${error ? " error" : ""}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.add("hidden"), 6000);
}
function logout() {
  token = "";
  sessionStorage.removeItem("pilot-token");
  $("#shell").classList.add("hidden");
  $("#login").classList.remove("hidden");
}
function strategyName(id) {
  return state.strategies.find((s) => s.id === id)?.name || id;
}
function badge(text, kind = "") {
  return `<span class="badge ${kind}">${esc(text)}</span>`;
}
function heading(title, subtitle, actions = "") {
  return `<div class="page-heading"><div><div class="eyebrow">ВАША СЕТЬ · ВАШИ ПРАВИЛА</div><h1>${esc(title)}</h1><p>${esc(subtitle)}</p></div><div class="heading-actions">${actions}</div></div>`;
}
function logo(p) {
  return `<div class="service-logo ${p.id === "discord" ? "discord" : ""}">${p.id === "youtube" ? "▶" : p.id === "discord" ? "◒" : esc(p.name[0])}</div>`;
}
function jobSummary() {
  const job = state.job;
  if (!job) return "Проверки ещё не выполнялись";
  if (!job.finished) return "Выполняется подбор";
  return job.selected
    ? `${strategyName(job.selected)} · ${job.profile}`
    : "Рабочий вариант не выбран";
}
function overview() {
  const c = state.config;
  const totalDomains = c.profiles.reduce((sum, p) => sum + p.domains.length, 0);
  return (
    heading(
      "Обзор сети",
      "Обход блокировок и доступность сервисов в одном месте.",
    ) +
    `<div class="status-card"><div class="status-main"><div class="status-orb">${state.running ? "↗" : "○"}</div><div><div class="eyebrow">СОСТОЯНИЕ ОБХОДА</div><h2>${state.running ? "Сеть под управлением" : "Обход остановлен"}</h2><p>${state.running ? "Обработка трафика на роутере · " + esc(c.wan) : "Запустите сервис, когда настройки готовы"}</p></div></div><button id="service-toggle" ${state.busy ? "disabled" : ""} class="${state.running ? "" : "primary"}">${state.running ? "Остановить" : "Запустить обход"} ${state.running ? "Ⅱ" : "↗"}</button></div>
  <div class="stats-grid"><div class="stat"><div class="stat-top">Подключённые профили <span class="stat-symbol">◫</span></div><div class="stat-value">${c.profiles.length}<em>сервиса</em></div><small>${totalDomains} доменов и их поддомены</small></div><div class="stat"><div class="stat-top">Автоподбор <span class="stat-symbol">⌁</span></div><div class="stat-value">${c.auto ? "Включён" : "Выключен"}</div><small>${c.auto ? `Проверка каждые ${c.interval_minutes} мин` : "Ручное управление стратегиями"}</small></div><div class="stat"><div class="stat-top">Совместимость <span class="stat-symbol">▱</span></div><div class="stat-value">TLS 1.2<em>и TLS 1.3</em></div><small>Отдельные пробы для каждого протокола</small></div></div>
  <div class="dashboard-columns"><div><div class="panel"><div class="panel-head"><h2>Ваши сервисы</h2><a href="#profiles">Все профили ↗</a></div>${c.profiles.map((p) => `<div class="service-row">${logo(p)}<div class="service-meta"><h3>${esc(p.name)}</h3><span>${esc(strategyName(p.strategy))} · ${p.domains.length} доменов</span></div>${badge(p.strategy === "none" ? "Без вмешательства" : "Профиль настроен", "neutral")}</div>`).join("")}</div><div class="panel"><div class="panel-head"><h2>Одна настройка — для всех устройств</h2>${badge("НА РОУТЕРЕ", "neutral")}</div><div class="compat-grid"><div class="compat-item"><div class="compat-icon">▯</div><strong>Телефон</strong><small>Android и iOS<br>Без отдельного клиента</small></div><div class="compat-item"><div class="compat-icon">▱</div><strong>Компьютер</strong><small>Windows, macOS, Linux<br>Обход в локальной сети</small></div><div class="compat-item"><div class="compat-icon">▣</div><strong>Телевизор</strong><small>webOS / Smart TV<br>Проверка TLS 1.2</small></div></div><div class="info-note">Проба TLS 1.2 проверяет протокол, но не заменяет испытание приложения YouTube на телевизоре. Видео и голос проверяются на устройствах отдельно.</div></div></div>
  <div><div class="panel"><div class="panel-head"><h2>Параметры сети</h2><span class="subtle">↗</span></div><div class="info-line"><span>Интерфейс WAN</span><span>${esc(c.wan || "Не задан")}</span></div><div class="info-line"><span>IPv4</span>${badge("Включён", "neutral")}</div><div class="info-line"><span>IPv6</span>${badge(c.ipv6 ? "Включён" : "Выключен", "neutral")}</div><div class="info-line"><span>QUIC / HTTP 3</span>${badge(c.quic ? "Обработка включена" : "Без обработки", "neutral")}</div><div class="info-line"><span>Исключения</span><span>${c.exclusions.length} доменов</span></div><div class="info-line"><span>Аппаратное ускорение</span><span>${c.ppe ? "PPE · окно handshake" : "Не управляется"}</span></div></div><div class="panel"><div class="panel-head"><h2>Последний подбор</h2><a href="#tune">Открыть ↗</a></div><p>${esc(jobSummary())}</p><div class="info-note">Стратегия сохраняется только после успешных повторных проверок. Если подходящего варианта нет — выполняется откат.</div></div></div></div>`
  );
}
function profiles() {
  return (
    heading(
      "Сервисы и домены",
      "У каждого сервиса — своя стратегия, список доменов и контрольные запросы.",
    ) +
    `<div class="profile-grid">${state.config.profiles.map((p) => `<article class="panel"><div class="profile-title">${logo(p)}<div><h2>${esc(p.name)}</h2><small>${p.domains.length} доменов · ${p.targets.length} контрольных адреса</small></div></div><label for="strategy-${esc(p.id)}">Текущая стратегия</label><select id="strategy-${esc(p.id)}">${state.strategies.map((s) => `<option value="${esc(s.id)}" ${s.id === p.strategy ? "selected" : ""}>${esc(s.name)}</option>`).join("")}</select><p class="profile-description">${esc(state.strategies.find((s) => s.id === p.strategy)?.description)}</p><details><summary>Домены и проверки</summary><label for="domains-${esc(p.id)}">Один домен на строку, поддомены включаются автоматически</label><textarea rows="7" id="domains-${esc(p.id)}">${esc(p.domains.join("\n"))}</textarea><div class="field"></div><label>Контрольные запросы</label>${p.targets.map((t, i) => `<div class="target-editor" data-profile="${esc(p.id)}" data-target="${i}"><label>Адрес HTTPS</label><input type="url" data-field="url" value="${esc(t.url)}"><div class="target-small"><div><label>HTTP</label><input type="number" min="200" max="399" data-field="status" value="${t.status}"></div><div><label>Фрагмент ответа</label><input data-field="contains" value="${esc(t.contains || "")}" placeholder="Необязательно"></div></div></div>`).join("")}<small>Каждый адрес проверяется с TLS 1.2 и TLS 1.3. URL проверки должен входить в домены этого профиля.</small></details><div class="profile-buttons"><button data-save-profile="${esc(p.id)}" ${state.busy ? "disabled" : ""}>Сохранить</button><button data-tune-profile="${esc(p.id)}" class="primary" ${!state.running || state.busy ? "disabled" : ""}>Подобрать ↗</button></div></article>`).join("")}</div><div class="info-note">Списки задают область обработки. Остальные сайты продолжают работать обычным способом. Discord Voice — отдельная экспериментальная настройка UDP; успешная HTTPS-проверка не означает, что голос работает.</div>`
  );
}
function tune() {
  return (
    heading(
      "Подбор стратегий",
      "Проверяем соединения, а не угадываем по названию провайдера.",
    ) +
    `<div class="tune-intro"><div class="step-card"><b>01 / ИСХОДНОЕ СОСТОЯНИЕ</b><strong>Проверка без обхода</strong><p>Прямые соединения помогают понять, требуется ли вмешательство.</p></div><div class="step-card"><b>02 / ПОИСК</b><strong>TLS 1.2 + TLS 1.3</strong><p>Применяем варианты и повторяем контрольные запросы.</p></div><div class="step-card"><b>03 / ПОДТВЕРЖДЕНИЕ</b><strong>Сохранение или откат</strong><p>Перезапускаем движок и снова проверяем выбранный вариант.</p></div></div><div class="panel"><div class="tune-control"><div class="field"><label for="tune-profile">Сервис</label><select id="tune-profile">${state.config.profiles.map((p) => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</select></div><button id="tune-start" class="primary" ${!state.running || state.busy ? "disabled" : ""}>Начать подбор ↗</button><button id="tune-cancel" ${!state.job || state.job.finished ? "disabled" : ""}>Отменить</button></div><div class="info-note">Во время подбора движок несколько раз перезапускается. Возможны краткие перебои у активных соединений. QUIC, голос Discord и фактическое воспроизведение видео требуют отдельных испытаний.</div></div><div id="job-panel">${jobPanel()}</div>`
  );
}
function jobPanel() {
  const j = state.job;
  if (!j)
    return `<div class="panel empty"><div class="empty-icon">⌁</div><h3>Найдём рабочий маршрут</h3><p>Запустите обход и выберите сервис для проверки.<br>Здесь появятся реальные результаты каждого запроса.</p></div>`;
  const stages = {
    baseline: "Проверяем соединение без обхода",
    confirm: "Подтверждаем результат после перезапуска",
    done: "Подбор завершён",
    failed: "Подбор остановлен с откатом",
  };
  return `<div class="panel"><div class="panel-head"><h2>Результаты · ${esc(j.profile)}</h2>${badge(j.finished ? (j.selected ? "Сохранено" : "Откат") : "В процессе", j.finished && !j.selected ? "warn" : "")}</div><div class="job-progress">${j.finished ? "" : '<span class="pulse"></span>'}${esc(stages[j.stage] || `Проверяем: ${strategyName(j.stage)}`)}</div>${j.error ? `<div class="notice danger">${esc(j.error)}</div>` : ""}${j.trials.map((t) => `<div class="trial"><div class="trial-top"><strong>${esc(t.strategy === "direct" ? "Без обхода" : t.strategy.startsWith("confirm:") ? "Повторная проверка: " + strategyName(t.strategy.slice(8)) : strategyName(t.strategy))}</strong>${badge(t.passed ? "Все пробы успешны" : "Проверка не пройдена", t.passed ? "" : "fail")}</div>${t.error ? `<p class="fail-text">${esc(t.error)}</p>` : ""}${t.results.length ? `<table class="probe-table"><thead><tr><th>Адрес</th><th>Протокол</th><th>Время</th><th>Результат</th></tr></thead><tbody>${t.results.map((r) => `<tr><td>${esc(new URL(r.url).hostname)}<br><small>Попытка ${r.attempt}</small></td><td>TLS ${esc(r.tls)}<br><small>${r.family === "tcp6" ? "IPv6" : "IPv4"}</small></td><td>${r.duration_ms} мс</td><td class="${r.ok ? "ok-text" : "fail-text"}">${r.ok ? "✓ HTTP " + r.status : esc(r.error)}</td></tr>`).join("")}</tbody></table>` : ""}</div>`).join("")}</div>`;
}
function toggle(id, title, text, checked) {
  return `<div class="toggle-row"><div><label for="${id}">${esc(title)}</label><p>${esc(text)}</p></div><input type="checkbox" id="${id}" ${checked ? "checked" : ""}></div>`;
}
function settings() {
  const c = state.config;
  return (
    heading(
      "Настройки",
      "Сетевые параметры и правила автоматического подбора.",
    ) +
    `<form id="settings-form"><div class="settings-grid"><div class="panel"><div class="panel-head"><h2>Подключение</h2></div><div class="field"><label for="wan">WAN-интерфейс</label><input id="wan" value="${esc(c.wan)}" placeholder="Например: ppp0"><small>Имя активного интерфейса провайдера на роутере.</small></div>${toggle("ipv6", "Обрабатывать IPv6", "Включайте при наличии работающего IPv6 у провайдера.", c.ipv6)}${toggle("ppe", "Сохранять видимость handshake", "PPE: первые 30 пакетов без аппаратного ускорения.", c.ppe)}${toggle("quic", "Обрабатывать QUIC", "Статическая стратегия UDP/443. Подбор HTTPS её не проверяет.", c.quic)}${toggle("discord_voice", "Discord Voice · экспериментально", "Обработка UDP 50000–50099. Нужна проверка реального звонка.", c.discord_voice)}</div><div class="panel"><div class="panel-head"><h2>Автоматический подбор</h2></div>${toggle("auto", "Следить за доступностью", "После двух неудачных циклов проверок начинается новый подбор.", c.auto)}<div class="field"></div><div class="two-fields"><div class="field"><label for="interval">Интервал, мин</label><input id="interval" type="number" min="5" max="1440" value="${c.interval_minutes}"></div><div class="field"><label for="timeout">Таймаут запроса, с</label><input id="timeout" type="number" min="2" max="20" value="${c.timeout_seconds}"></div></div><div class="field"><label for="attempts">Попыток на протокол и адрес</label><input id="attempts" type="number" min="1" max="3" value="${c.attempts}"></div><div class="field"><label for="exclusions">Исключения · по одному домену на строку</label><textarea id="exclusions" rows="6" placeholder="example.com">${esc(c.exclusions.join("\n"))}</textarea><small>Исключённые домены не обрабатываются движком.</small></div></div></div><div class="form-actions"><button type="submit" class="primary" ${state.busy ? "disabled" : ""}>Сохранить настройки ↗</button><small>Работающий сервис перезапустится. При ошибке настройки откатятся.</small></div></form>`
  );
}
function lines(value) {
  return value
    .split(/\r?\n/)
    .map((v) => v.trim().toLowerCase())
    .filter(Boolean);
}
async function refresh(render = false) {
  state = await api("status");
  updateChrome();
  if (render) renderPage();
  else if (page === "overview") {
    $("#content").innerHTML = overview();
    bindActions();
  } else if (page === "tune") {
    $("#job-panel").innerHTML = jobPanel();
    $("#tune-start").disabled = !state.running || state.busy;
    $("#tune-cancel").disabled = !state.job || !!state.job.finished;
  }
}
function updateChrome() {
  $("#dev-banner").classList.toggle(
    "hidden",
    !["development", "mockup"].includes(state.mode),
  );
  $("#dev-banner").textContent =
    state.mode === "mockup"
      ? "МАКЕТ · Все показатели и результаты ниже — демонстрационные. К вашему роутеру подключения нет."
      : "Режим разработки. Панель и проверки доступны; обработка трафика выключена.";
  $("#version").textContent = `v${state.version}`;
  $("#device-detail").textContent = state.config.wan
    ? `WAN · ${state.config.wan}`
    : "WAN не задан";
  $("#service-error").textContent = state.error || "";
  $("#service-error").classList.toggle("hidden", !state.error);
}
function renderPage() {
  page = location.hash.slice(1);
  if (!labels[page]) page = "overview";
  $("#breadcrumb").textContent = labels[page];
  document
    .querySelectorAll("[data-page]")
    .forEach((a) => a.classList.toggle("active", a.dataset.page === page));
  $("#content").innerHTML =
    page === "overview"
      ? overview()
      : page === "profiles"
        ? profiles()
        : page === "tune"
          ? tune()
          : page === "settings"
            ? settings()
            : heading(
                "Журнал",
                "События контроллера и движка. Последние 200 строк.",
                '<button id="refresh-logs">Обновить ↻</button>',
              ) +
              '<div class="panel"><pre id="log-view" class="log-view">Загрузка…</pre></div>';
  bindActions();
  if (page === "logs") loadLogs();
}
async function action(fn) {
  try {
    await fn();
    await refresh(true);
  } catch (e) {
    notify(e.message, true);
  }
}
function bindActions() {
  $("#service-toggle")?.addEventListener("click", () =>
    action(() => api("service", "POST", { enabled: !state.config.enabled })),
  );
  $("#tune-start")?.addEventListener("click", () =>
    action(() => api("tune", "POST", { profile: $("#tune-profile").value })),
  );
  $("#tune-cancel")?.addEventListener("click", () =>
    action(() => api("tune/cancel", "POST", {})),
  );
  $("#refresh-logs")?.addEventListener("click", loadLogs);
  document.querySelectorAll("[data-tune-profile]").forEach((button) =>
    button.addEventListener("click", () =>
      action(async () => {
        await api("tune", "POST", { profile: button.dataset.tuneProfile });
        location.hash = "tune";
      }),
    ),
  );
  document.querySelectorAll("[data-save-profile]").forEach((button) =>
    button.addEventListener("click", () =>
      action(async () => {
        const id = button.dataset.saveProfile,
          c = structuredClone(state.config),
          p = c.profiles.find((p) => p.id === id);
        p.strategy = $(`#strategy-${id}`).value;
        p.domains = lines($(`#domains-${id}`).value);
        document
          .querySelectorAll(`[data-profile="${id}"]`)
          .forEach((editor) => {
            const t = p.targets[Number(editor.dataset.target)];
            t.url = editor.querySelector("[data-field=url]").value.trim();
            t.status = Number(
              editor.querySelector("[data-field=status]").value,
            );
            t.contains = editor.querySelector("[data-field=contains]").value;
          });
        await api("config", "PUT", c);
        notify("Профиль сохранён");
      }),
    ),
  );
  $("#settings-form")?.addEventListener("submit", (event) => {
    event.preventDefault();
    action(async () => {
      const c = structuredClone(state.config);
      c.wan = $("#wan").value.trim();
      ["ipv6", "ppe", "quic", "discord_voice", "auto"].forEach(
        (key) => (c[key] = $(`#${key}`).checked),
      );
      c.interval_minutes = Number($("#interval").value);
      c.timeout_seconds = Number($("#timeout").value);
      c.attempts = Number($("#attempts").value);
      c.exclusions = lines($("#exclusions").value);
      await api("config", "PUT", c);
      notify("Настройки сохранены");
    });
  });
}
async function loadLogs() {
  try {
    const logs = await api("logs");
    if ($("#log-view"))
      $("#log-view").textContent = logs.length
        ? logs.join("\n")
        : "Событий пока нет.";
  } catch (e) {
    notify(e.message, true);
  }
}
async function enter() {
  await refresh();
  sessionStorage.setItem("pilot-token", token);
  $("#login").classList.add("hidden");
  $("#shell").classList.remove("hidden");
  renderPage();
}
$("#login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  token = $("#token").value.trim();
  try {
    await enter();
    $("#token").value = "";
    $("#login-error").textContent = "";
  } catch (e) {
    $("#login-error").textContent = e.message;
  }
});
$("#logout").addEventListener("click", logout);
window.addEventListener("hashchange", () => {
  if (state && token) renderPage();
});
if (token)
  enter().catch((e) => {
    logout();
    $("#login-error").textContent = e.message;
  });
setInterval(() => {
  if (token && state && !document.hidden)
    refresh().catch((e) => notify(e.message, true));
}, 4000);
