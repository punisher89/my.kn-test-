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
  device: "Роутер",
  updates: "Обновления",
  connect: "Первый запуск",
  adaptive: "Коробки обхода",
  strategies: "Каталог методов",
};
let state,
  page = "overview",
  token = sessionStorage.getItem("pilot-token") || "",
  toastTimer;
let signedIn = false,
  authInfo = { configured: true },
  networkInfo;

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
async function logout() {
  signedIn = false;
  adaptiveData = null;
  adaptiveDrafts.clear();
  adaptiveDetails.clear();
  profileDrafts.clear();
  profileOpen.clear();
  checkOpen.clear();cpuSamples.length=0;deviceData=null;
  serviceCatalogQuery = ""; serviceCatalogOpen = false; selectedTuneProfile = "";
  token = "";
  sessionStorage.removeItem("pilot-token");
  $("#shell").classList.add("hidden");
  $("#login").classList.remove("hidden");
  $("#login-submit").disabled = true;
  try {
    await fetch("/auth/logout", { method: "POST" });
    await loadAuth();
  } catch (e) {
    $("#login-error").textContent = e.message;
  } finally {
    $("#login-submit").disabled = false;
  }
}
function strategyName(id) {
  return state.strategies.find((s) => s.id === id)?.name || id;
}
const familyLabels = {
  direct: "Без вмешательства",
  split: "Разделение и порядок",
  fake: "Fake",
  hostfake: "HostFake",
  overlap: "SEQ overlap",
  other: "Другие методы",
};
function methodGroups() {
  return Object.entries(familyLabels)
    .map(([id, name]) => ({
      id,
      name,
      methods: state.strategies.filter(
        (s) => (s.family || (s.id === "none" ? "direct" : "other")) === id,
      ),
    }))
    .filter((g) => g.methods.length);
}
function strategyOptions(selected) {
  return methodGroups()
    .map(
      (g) =>
        `<optgroup label="${esc(g.name)}">${g.methods.map((s) => `<option value="${esc(s.id)}" ${s.id === selected ? "selected" : ""}>${esc(s.name)}</option>`).join("")}</optgroup>`,
    )
    .join("");
}
let methodQuery="", methodFamily="all";
const methodOpen=new Map(),trialOpen=new Map();
function methodResults(){
 const query=methodQuery.trim().toLowerCase();
 return methodGroups().filter(g=>methodFamily==="all"||methodFamily===g.id).map(g=>({...g,methods:g.methods.filter(m=>[m.id,m.name,m.description,g.name].join(" ").toLowerCase().includes(query))})).filter(g=>g.methods.length).map(g=>`<details class="panel method-family" data-method-view="family/${esc(g.id)}" ${methodOpen.get("family/"+g.id)||query?"open":""}><summary>${esc(g.name)} · ${g.methods.length}</summary>${g.methods.map(m=>`<details class="method-item" data-method-view="method/${esc(m.id)}" ${methodOpen.get("method/"+m.id)?"open":""}><summary>${esc(m.name)}</summary><p>${esc(m.description)}</p>${m.actions?.length?`<pre>${esc(m.actions.join("\n"))}</pre>`:'<p>Обычное соединение без изменения пакетов.</p>'}</details>`).join("")}</details>`).join("")||'<p class="subtle">Методы не найдены.</p>';
}
function strategyCatalog(){return heading("Каталог методов","Семейства и параметры раскрываются по нажатию. Наличие метода не гарантирует доступ.")+`<div class="panel method-toolbar"><label>Поиск<input id="method-search" type="search" value="${esc(methodQuery)}" placeholder="Название или семейство"></label><label>Семейство<select id="method-family"><option value="all">Все семейства</option>${methodGroups().map(g=>`<option value="${esc(g.id)}" ${methodFamily===g.id?"selected":""}>${esc(g.name)}</option>`).join("")}</select></label></div><div class="method-list" id="method-results">${methodResults()}</div>`;}
function bindMethodViews(){document.querySelectorAll("[data-method-view]").forEach(el=>el.addEventListener("toggle",()=>methodOpen.set(el.dataset.methodView,el.open)));}
function bindTrials(){document.querySelectorAll("[data-trial]").forEach(el=>el.addEventListener("toggle",()=>trialOpen.set(el.dataset.trial,el.open)));}
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
  const c=state.config;
  return heading("Состояние сети", "Обход и доступность сервисов под контролем.")+
    `<div class="status-card"><div class="status-main"><span class="dot"></span><strong>${state.running?"ОБХОД ВКЛЮЧЁН":"ОБХОД ОСТАНОВЛЕН"}</strong><small>WAN · ${esc(c.wan||"не задан")}</small></div><button id="service-toggle" ${state.busy?"disabled":""}>${state.running?"Остановить":"Запустить обход"}</button></div>
    ${!c.enabled&&!state.job?'<div class="panel welcome"><h2>Первый запуск</h2><a class="button-link primary" href="#connect">Настроить подключение</a></div>':""}
    <div id="network-telemetry" class="network-telemetry" aria-live="off">${deviceData?telemetryMarkup(deviceData):"Загрузка показателей роутера…"}</div>
    <div class="dashboard-columns"><section class="panel"><div class="panel-head"><h2>Сервисы</h2><a href="#profiles">Управление →</a></div><div class="service-check-list">${c.profiles.map(p=>`<div class="service-row">${logo(p)}<div class="service-meta"><h3>${esc(p.name)}</h3><span>${esc(strategyName(p.strategy))} · ${p.domains.length} доменов</span></div><div class="service-check-status" data-check-status="${esc(p.id)}">${checkStatus(p)}</div><button data-check-profile="${esc(p.id)}">Проверить</button></div><div data-check-report="${esc(p.id)}">${checkReport(p)}</div>`).join("")}</div><p class="info-note">Проверяются контрольные HTTPS-адреса сервиса. Ответ не подтверждает видео, звонки и работу всех доменов.</p></section><aside>${loadControlPanel()}<p class="subtle"><a href="#tune">Последний подбор: ${esc(jobSummary())} →</a></p></aside></div>
    <section class="panel"><div class="panel-head"><h2>Коробки обхода</h2><a href="#adaptive">Открыть →</a></div><div id="overview-boxes">Загрузка наблюдений…</div></section>`;
}
function loadControlPanel(){const c=state.config;return `<section class="panel load-control"><div class="panel-head"><h2>Контроль нагрузки</h2><a href="#settings">Настроить →</a></div><div id="cpu-chart">${cpuChart()}</div><div class="info-line"><span>Пауза между сменами</span><strong>${c.adaptive_cooldown_seconds} с</strong></div><div class="info-line"><span>Лимит на домен и контекст</span><strong>${c.adaptive_max_switches} / 10 мин</strong></div><div class="info-line"><span>Общий лимит движка</span><strong>${c.adaptive_global_max_switches??30} / 10 мин</strong></div><div class="info-line"><span>Защита фоновых проверок</span>${badge(c.probe_load_guard!==false?"Включена":"Выключена",c.probe_load_guard!==false?"":"warn")}</div><p>${state.background_hold?"Фоновые проверки отложены: "+esc(state.background_hold)+". Повторная оценка через 30 с.":"Проверки идут последовательно. Работающий обход продолжается при паузе фоновых задач."}</p><small>CPU и память общие для роутера. Коробки не являются отдельными процессами.</small></section>`;}
const checkPending=new Set(),checkOpen=new Map();
function checkStatus(p){const r=state.checks?.[p.id];if(checkPending.has(p.id))return badge("Проверяется","warn");if(!r)return badge("Не проверялся","neutral");return badge(r.stale?"Проверка устарела":!r.complete?"Не завершена":r.passed?"Есть HTTPS-ответ":"Есть ошибки",r.stale?"neutral":r.passed?"":"warn");}
function checkReport(p){const r=state.checks?.[p.id];if(!r)return "";const hosts=new Set(r.results.map(v=>new URL(v.url).hostname));const uncovered=p.domains.filter(d=>![...hosts].some(h=>h===d||h.endsWith("."+d)));return `<details class="check-report" data-check-view="${esc(p.id)}" ${checkOpen.get(p.id)?"open":""}><summary>Последняя проверка · ${esc(new Date(r.finished).toLocaleString("ru-RU"))} · ${r.results.filter(v=>v.ok).length}/${r.results.length} проб</summary><p>${r.stale?"Конфигурация или состояние службы изменились — повторите проверку. ":""}${!r.running?"Проверка при остановленном обходе. ":""}Профиль: ${esc(strategyName(r.strategy))}${r.adaptive?"; автоподбор по трафику включён, метод может отличаться по доменам и протоколам":""}.</p>${r.error?`<p class="fail-text">${esc(r.error)}</p>`:""}<div class="table-scroll"><table class="probe-table"><thead><tr><th>HTTPS-адрес</th><th>TLS / IP</th><th>Ответ / время</th><th>Результат</th></tr></thead><tbody>${r.results.map(v=>`<tr><td>${esc(v.url)}</td><td>${esc(v.tls)} / ${esc(v.family)}</td><td>${v.status||"—"} · ${v.duration_ms} мс</td><td class="${v.ok?"ok-text":"fail-text"}">${v.ok?"Есть ответ":esc(v.error||"Ошибка")}</td></tr>`).join("")}</tbody></table></div><small>Контрольные адреса не покрывают весь сервис.${uncovered.length?" Домены без отдельной HTTPS-пробы: "+esc(uncovered.join(", "))+".":""} Отчёт хранится до перезапуска Pilot. Проверка не запускает подбор; её трафик может наблюдаться включённым адаптивным движком.</small><a class="button-link" href="#adaptive">Наблюдаемые методы доменов →</a></details>`;}
function bindCheckActions(root=document){root.querySelectorAll("[data-check-view]").forEach(el=>el.addEventListener("toggle",()=>checkOpen.set(el.dataset.checkView,el.open)));root.querySelectorAll("[data-check-profile]").forEach(button=>{button.disabled=state.busy||checkPending.size>0;button.addEventListener("click",async()=>{const id=button.dataset.checkProfile;if(checkPending.size)return;checkPending.add(id);updateCheckReports();try{await api("check","POST",{profile:id});}catch(e){notify(e.message,true);}finally{checkPending.delete(id);if(signedIn){try{await refresh();}catch(e){notify(e.message,true);}updateCheckReports();}}});});}
function updateCheckReports(){document.querySelectorAll("[data-check-profile]").forEach(el=>{el.disabled=state.busy||checkPending.size>0;el.textContent=checkPending.has(el.dataset.checkProfile)?"Проверяется…":"Проверить";});for(const p of state.config.profiles){const status=$(`[data-check-status="${p.id}"]`),report=$(`[data-check-report="${p.id}"]`);if(status)status.innerHTML=checkStatus(p);if(report){report.innerHTML=checkReport(p);report.querySelectorAll("[data-check-view]").forEach(el=>el.addEventListener("toggle",()=>checkOpen.set(el.dataset.checkView,el.open)));}}}
const profileDrafts = new Map(), profileOpen = new Map();
let serviceCatalogOpen = false, serviceCatalogQuery = "", selectedTuneProfile = "", serviceMutationPending = false;
function presetNote(id) {
  return (state.service_presets || []).find((s) => s.profile.id === id)?.note || "HTTPS-пробы не подтверждают работу приложения, видео и голоса.";
}
function servicePresetState(p) {
  const existing = state.config.profiles.some((v) => v.id === p.id);
  const conflict = state.config.profiles.some((v) => v.domains.some((d) => p.domains.some((other) => d === other || d.endsWith("." + other) || other.endsWith("." + d))));
  return { existing, conflict, blocked: existing || conflict || state.config.profiles.length >= 12 || state.busy || serviceMutationPending };
}
function updateServiceButtons() {
  document.querySelectorAll("[data-add-service]").forEach((button) => {
    const preset = (state.service_presets || []).find((p) => p.profile.id === button.dataset.addService);
    button.disabled = !preset || servicePresetState(preset.profile).blocked;
  });
  document.querySelectorAll("[data-domain-sync]").forEach(b=>{b.disabled=state.busy||serviceMutationPending;});
  const customButton=$("#custom-service-form button[type=submit]");if(customButton)customButton.disabled=state.busy||serviceMutationPending||state.config.profiles.length>=12;
  document.querySelectorAll("[data-profile-card]").forEach(card=>{const p=state.config.profiles.find(p=>p.id===card.dataset.profileCard);if(!p)return;const summary=card.querySelector(".profile-summary small");if(summary)summary.textContent=strategyName(p.strategy)+" · доменов: "+p.domains.length;if(!profileDrafts.has(p.id)){const box=$(`#domains-${p.id}`);if(box)box.value=p.domains.join("\n");const select=$(`#strategy-${p.id}`);if(select)select.value=p.strategy;}});
  document.querySelectorAll("[data-save-profile]").forEach((b) => { b.disabled = state.busy || serviceMutationPending; });
  document.querySelectorAll("[data-remove-service]").forEach((b) => { b.disabled = state.busy || serviceMutationPending || state.config.profiles.length <= 1; });
}
async function serviceAction(fn) {
  if (serviceMutationPending) return;
  serviceMutationPending = true;
  updateServiceButtons();
  try { await action(fn); } finally { serviceMutationPending = false; updateServiceButtons(); }
}
function presetRows() {
  const query = serviceCatalogQuery.trim().toLowerCase();
  const presets = (state.service_presets || []).filter((s) => [s.profile.name, s.profile.id, s.category, ...(s.aliases || []), ...s.profile.domains].join(" ").toLowerCase().includes(query));
  return presets.map((s) => {
    const p = s.profile, { existing, conflict, blocked } = servicePresetState(s.profile);
    return `<div class="preset-row" data-service="${esc(p.id)}"><div><strong>${esc(p.name)}</strong><small>${esc(s.category)} · доменов: ${p.domains.length}</small><details><summary>Домены и границы проверки</summary><p class="subtle">${esc(s.note)}</p><p class="domain-list">${p.domains.map((d) => `<span>${esc(d)}</span>`).join("")}</p>${p.targets.map((t) => `<small>${esc(t.url)} · HTTP ${t.status} · ${esc(t.contains || "")}</small>`).join("")}</details></div>${existing && page === "tune" ? `<button data-preset-select="${esc(p.id)}">Выбрать</button>` : `<button data-add-service="${esc(p.id)}" ${blocked ? "disabled" : ""}>${existing ? "Добавлен" : conflict ? "Домены заняты" : "Добавить"}</button>`}</div>`;
  }).join("") || '<p class="subtle">Сервис не найден.</p>';
}
function serviceCatalog() {
  return `<details class="panel service-catalog" id="service-catalog" ${serviceCatalogOpen ? "open" : ""}><summary>Популярные сервисы · каталог: ${(state.service_presets || []).length}</summary><p class="subtle">Добавьте нужные профили. Добавление перезапускает работающий движок; новый профиль начинает без вмешательства. Доступность зависит от сети, региона и аккаунта. Для подбора используется HTTPS-проверка, а не проверка всего приложения.</p><label>Найти сервис<input type="search" id="service-search" value="${esc(serviceCatalogQuery)}" placeholder="Название сервиса или домен"></label><small>Подключено профилей: ${state.config.profiles.length} из 12</small><div id="service-preset-list">${presetRows()}</div></details>`;
}
function profileCard(p) {
  const edit = profileDrafts.get(p.id) || p;
  return `<details class="panel service-profile" data-profile-card="${esc(p.id)}" ${profileOpen.get(p.id) ? "open" : ""}><summary><span class="profile-summary">${logo(p)}<span><strong>${esc(p.name)}</strong><small>${esc(strategyName(p.strategy))} · доменов: ${p.domains.length}</small></span></span></summary><div class="profile-editor"><p class="subtle">${esc(presetNote(p.id))}</p><label for="strategy-${esc(p.id)}">Текущая стратегия</label><select id="strategy-${esc(p.id)}">${strategyOptions(edit.strategy)}</select><details><summary>Домены и проверки</summary><label for="domains-${esc(p.id)}">Один домен на строку, поддомены включаются автоматически</label><textarea rows="4" id="domains-${esc(p.id)}">${esc(edit.domains.join("\n"))}</textarea><div class="field"></div><label>Контрольные запросы</label>${edit.targets.map((t, i) => `<div class="target-editor" data-profile="${esc(p.id)}" data-target="${i}"><label>Адрес HTTPS</label><input type="url" data-field="url" value="${esc(t.url)}"><div class="target-small"><div><label>HTTP</label><input type="number" min="200" max="399" data-field="status" value="${t.status}"></div><div><label>Фрагмент ответа</label><input data-field="contains" value="${esc(t.contains || "")}" placeholder="Необязательно"></div></div></div>`).join("")}<small>Каждый адрес проверяется с TLS 1.2 и TLS 1.3. HTTP 403/451 и ошибки сертификата не считаются успешным обходом. Контрольные адреса можно изменить под нужный сценарий.</small></details>${domainSourceEditor(edit)}<div class="profile-buttons"><button data-save-profile="${esc(p.id)}" ${state.busy ? "disabled" : ""}>Сохранить</button><button data-check-profile="${esc(p.id)}">Проверить сервис</button><button data-tune-profile="${esc(p.id)}" class="primary" ${!state.running || state.busy ? "disabled" : ""}>Подобрать ↗</button><button data-remove-service="${esc(p.id)}" ${state.busy || state.config.profiles.length <= 1 ? "disabled" : ""}>Удалить профиль</button></div><div data-check-status="${esc(p.id)}">${checkStatus(p)}</div><div data-check-report="${esc(p.id)}">${checkReport(p)}</div></div></details>`;
}
function domainSourceEditor(p){
 const preset=(state.service_presets||[]).some(s=>s.profile.id===p.id),fromPilot=p.domain_source?.startsWith("pilot:");
 const source=p.domain_source||"",mode=source.startsWith("pilot:")?"pilot":source?"url":"";
 return `<details class="domain-source-editor"><summary>Домены из интернета</summary><label>Источник<select id="source-mode-${esc(p.id)}"><option value="">Вручную</option>${preset||fromPilot?`<option value="pilot" ${mode==="pilot"?"selected":""}>Подписанный каталог Pilot</option>`:""}<option value="url" ${mode==="url"?"selected":""}>HTTPS-ссылка на список</option></select></label><label>Ссылка<input id="source-url-${esc(p.id)}" type="url" value="${esc(mode==="url"?source:"")}" placeholder="https://example.com/domains.txt"></label><label class="check-label"><input type="checkbox" id="source-auto-${esc(p.id)}" ${p.auto_domains?"checked":""}>Обновлять раз в сутки</label><details><summary>Контрольная сумма и исключения</summary><label>SHA-256 файла, необязательно<input id="source-sha-${esc(p.id)}" value="${esc(p.domain_sha256||"")}" maxlength="64"></label><label>Не добавлять из источника<textarea id="source-exclusions-${esc(p.id)}" rows="3">${esc((p.source_exclusions||[]).join("\n"))}</textarea></label></details><p class="subtle">Загружаются новые домены; охват ваших доменов сохраняется. Вложенные домены объединяются с родительским. Для удаления домена вручную добавьте его в исключения источника или отключите автообновление. Применение новых доменов кратко перезапустит работающий движок.</p><small>Последнее обновление: ${Date.parse(p.domains_updated)>0?esc(new Date(p.domains_updated).toLocaleString("ru-RU")):"ещё не выполнялось"}</small><button data-domain-sync="${esc(p.id)}">Сохранить и загрузить домены</button></details>`;
}
function customServiceForm(){return `<details class="panel custom-service"><summary>Добавить свой сервис</summary><form id="custom-service-form"><div class="two-fields"><label>Название<input name="name" maxlength="80" required></label><label>Контрольный HTTPS-адрес<input name="url" type="url" required placeholder="https://example.com/"></label></div><label>Домены, по одному на строку<textarea name="domains" rows="3" placeholder="example.com"></textarea></label><label>Ожидаемый фрагмент ответа<input name="contains" required maxlength="512" placeholder="Текст, подтверждающий нужный ответ"></label><details><summary>Домены из интернета</summary><label>HTTPS-ссылка на список<input name="source" type="url" placeholder="https://example.com/domains.txt"></label><label class="check-label"><input type="checkbox" name="auto" checked>Обновлять раз в сутки, если задан источник</label></details><p class="subtle">Нужен список доменов, по одному на строку. Если домены не введены, начальный домен берётся из контрольного адреса. Новый профиль начинает без вмешательства.</p><button class="primary" type="submit">Добавить сервис</button></form></details>`;}
function profiles() {
  return heading("Сервисы и домены", "Выберите сервис из каталога. Раскройте профиль для стратегии, доменов и проверок.") + serviceCatalog() + customServiceForm() + `<div class="service-profile-list">${state.config.profiles.map(profileCard).join("")}</div>`;
}
function readProfileDraft(id) {
  const p = structuredClone(state.config.profiles.find((p) => p.id === id));
  p.strategy = $(`#strategy-${id}`).value;
  p.domains = lines($(`#domains-${id}`).value);
  document.querySelectorAll(`[data-profile="${id}"]`).forEach((editor) => {
    const t = p.targets[Number(editor.dataset.target)];
    t.url = editor.querySelector("[data-field=url]").value.trim();
    t.status = Number(editor.querySelector("[data-field=status]").value);
    t.contains = editor.querySelector("[data-field=contains]").value;
  });
  const mode=$(`#source-mode-${id}`)?.value;
  if(mode!==undefined){p.domain_source=mode==="pilot"?(p.domain_source?.startsWith("pilot:")?p.domain_source:"pilot:"+id):mode==="url"?$(`#source-url-${id}`).value.trim():"";p.auto_domains=Boolean(p.domain_source&&$(`#source-auto-${id}`).checked);p.domain_sha256=$(`#source-sha-${id}`).value.trim().toLowerCase();p.source_exclusions=lines($(`#source-exclusions-${id}`).value);}
  return p;
}
function bindPresetActions(root) {
  root.querySelectorAll("[data-add-service]").forEach((button) => button.addEventListener("click", async () => {
    button.disabled = true;
    try { await serviceAction(async () => {
      await api("services", "POST", { id: button.dataset.addService });
      if (page === "tune") selectedTuneProfile = button.dataset.addService;
      notify("Профиль добавлен. Можно запустить подбор или наблюдать новый трафик.");
    }); } finally { button.disabled = false; }
  }));
  root.querySelectorAll("[data-preset-select]").forEach((button) => button.addEventListener("click", () => {
    selectedTuneProfile = button.dataset.presetSelect;
    $("#tune-profile").value = selectedTuneProfile;
    $("#tune-profile-note").textContent = presetNote(selectedTuneProfile);
    $("#tune-profile").focus();
  }));
}
function tune() {
  return (
    heading(
      "Подбор стратегий",
      "Проверяем соединения, а не угадываем по названию провайдера.",
    ) +
    serviceCatalog() + `<div class="tune-intro"><div class="step-card"><b>01 / ИСХОДНОЕ СОСТОЯНИЕ</b><strong>Проверка без обхода</strong><p>Прямые соединения помогают понять, требуется ли вмешательство.</p></div><div class="step-card"><b>02 / ПОИСК</b><strong>TLS 1.2 + TLS 1.3</strong><p>Применяем варианты и повторяем контрольные запросы.</p></div><div class="step-card"><b>03 / ПОДТВЕРЖДЕНИЕ</b><strong>Сохранение или откат</strong><p>Перезапускаем движок и снова проверяем выбранный вариант.</p></div></div><div class="panel"><div class="tune-control"><div class="field"><label for="tune-profile">Сервис</label><select id="tune-profile">${state.config.profiles.map((p) => `<option value="${esc(p.id)}" ${p.id === selectedTuneProfile ? "selected" : ""}>${esc(p.name)}</option>`).join("")}</select></div><button id="tune-start" class="primary" ${!state.running || state.busy ? "disabled" : ""}>Начать подбор ↗</button><button id="tune-cancel" ${!state.job || state.job.finished ? "disabled" : ""}>Отменить</button></div><div class="info-note">Во время подбора движок несколько раз перезапускается. Возможны краткие перебои у активных соединений. QUIC, голос Discord и фактическое воспроизведение видео требуют отдельных испытаний.</div><p id="tune-profile-note" class="subtle">${esc(presetNote(state.config.profiles.find((p) => p.id === selectedTuneProfile)?.id || state.config.profiles[0].id))}</p></div><div id="job-panel">${jobPanel()}</div>`
  );
}
function jobPanel(){
 const j=state.job;if(!j)return '<div class="panel empty"><h3>Результатов пока нет</h3><p>Выберите сервис и начните подбор.</p></div>';
 const stages={direct:"Проверка без обхода",starting:"Подготовка",confirm:"Повторная проверка",done:"Завершено"};
 return `<div class="panel"><div class="panel-head"><h2>Результаты · ${esc(j.profile)}</h2>${badge(j.finished?(j.selected?"Сохранено":"Откат"):"В процессе",j.finished&&!j.selected?"warn":"neutral")}</div><p class="job-progress">${esc(stages[j.stage]||strategyName(j.stage))}</p>${j.error?`<div class="notice danger">${esc(j.error)}</div>`:""}<div class="trial-list">${j.trials.map((t,i)=>{
 const key=j.started+"/"+i,passed=t.results.filter(r=>r.ok).length;
 const name=t.strategy==="direct"?"Без обхода":t.strategy.startsWith("confirm:")?"Подтверждение: "+strategyName(t.strategy.slice(8)):strategyName(t.strategy);
 return `<details class="trial-compact" data-trial="${esc(key)}" ${trialOpen.get(key)?"open":""}><summary><strong>${esc(name)}</strong><span>${passed}/${t.results.length} проб</span>${badge(t.passed?"Успешно":"Не пройдена",t.passed?"":"fail")}</summary>${t.error?`<p class="fail-text">${esc(t.error)}</p>`:""}${t.results.length?`<div class="table-scroll"><table class="probe-table"><thead><tr><th>Адрес</th><th>TLS / IP</th><th>Время</th><th>Результат</th></tr></thead><tbody>${t.results.map(r=>`<tr><td>${esc(r.url)}<br><small>Попытка ${r.attempt}</small></td><td>${esc(r.tls)} · ${r.family==="tcp6"?"IPv6":"IPv4"}</td><td>${r.duration_ms} мс</td><td>${r.ok?"HTTP "+r.status:esc(r.error)}</td></tr>`).join("")}</tbody></table></div>`:""}</details>`;
 }).join("")}</div></div>`;
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
    `<form id="settings-form"><div class="settings-grid"><div class="panel"><div class="panel-head"><h2>Подключение</h2></div><div class="field"><label for="wan">WAN-интерфейс</label><input id="wan" value="${esc(c.wan)}" placeholder="Например: ppp0"><small>Имя активного интерфейса провайдера на роутере.</small></div>${toggle("ipv6", "Обрабатывать IPv6", "Включайте при наличии работающего IPv6 у провайдера.", c.ipv6)}${toggle("ppe", "Сохранять видимость handshake", "Первые пакеты без аппаратного ускорения: окно 48 при автоподборе по трафику, иначе 30.", c.ppe)}${toggle("quic", "Обрабатывать QUIC", "Статическая стратегия UDP/443. Подбор HTTPS её не проверяет.", c.quic)}${toggle("discord_voice", "Discord Voice · экспериментально", "Обработка UDP 50000–50099. Нужна проверка реального звонка.", c.discord_voice)}</div><div class="panel"><div class="panel-head"><h2>Автоматический подбор</h2></div>${toggle("adaptive", "Автоподбор по трафику", "Обнаруживать повторные ошибки HTTPS и менять метод отдельно для каждого сайта. Сохранённый метод используется первым.", c.adaptive)}<div class="info-note">Новые наблюдения и выбранные методы — в разделе «Коробки обхода». Сетевые ошибки не всегда означают блокировку.</div>${toggle("adaptive_reuse", "Использовать опыт коробок", "После повторных сбоев сначала пробовать до трёх методов, давших ответ в похожем контексте за последние семь дней.", c.adaptive_reuse)}<div class="two-fields"><div class="field"><label for="adaptive-cooldown">Пауза между сменами, с</label><input id="adaptive-cooldown" type="number" min="10" max="300" value="${c.adaptive_cooldown_seconds}"></div><div class="field"><label for="adaptive-max-switches">Максимум смен за 10 минут</label><input id="adaptive-max-switches" type="number" min="1" max="20" value="${c.adaptive_max_switches}"></div></div><div class="field"><label for="adaptive-global-max">Общий лимит смен движка за 10 минут</label><input id="adaptive-global-max" type="number" min="1" max="300" value="${c.adaptive_global_max_switches??30}"><small>Для всех коробок вместе. Рабочие методы сохраняются; при достижении лимита смена ждёт окончания окна.</small></div>${toggle("probe_load_guard","Защита фоновых проверок","Откладывать плановые проверки, подбор и загрузку каталогов при CPU ≥85%, высокой очереди задач или нехватке памяти. Работающий обход продолжается.",c.probe_load_guard!==false)}${toggle("auto_catalog", "Обновлять каталог стратегий", "Подписанный каталог Pilot: проверка раз в сутки. Новые варианты проверяются при подборе.", c.auto_catalog)}<div class="form-actions"><button type="button" id="catalog-update">Загрузить стратегии сейчас</button></div>${toggle("auto", "Следить за доступностью", "После двух неудачных циклов проверок начинается новый подбор.", c.auto)}<div class="field"></div><div class="two-fields"><div class="field"><label for="interval">Интервал, мин</label><input id="interval" type="number" min="5" max="1440" value="${c.interval_minutes}"></div><div class="field"><label for="timeout">Таймаут запроса, с</label><input id="timeout" type="number" min="2" max="20" value="${c.timeout_seconds}"></div></div><div class="field"><label for="attempts">Попыток на протокол и адрес</label><input id="attempts" type="number" min="1" max="3" value="${c.attempts}"></div><div class="field"><label for="exclusions">Исключения · по одному домену на строку</label><textarea id="exclusions" rows="6" placeholder="example.com">${esc(c.exclusions.join("\n"))}</textarea><small>Исключённые домены не обрабатываются движком.</small></div></div></div><div class="form-actions"><button type="submit" class="primary" ${state.busy ? "disabled" : ""}>Сохранить настройки ↗</button><small>Работающий сервис перезапустится. При ошибке настройки откатятся.</small></div></form>`
  );
}
function lines(value) {
  return value
    .split(/\r?\n/)
    .map((v) => v.trim().toLowerCase())
    .filter(Boolean);
}
function connectPage() {
  if (!networkInfo)
    return heading("Первый запуск", "Определяем доступные подключения…");
  const selected = state.config.wan || networkInfo.wan;
  return (
    heading(
      "Первый запуск",
      "Проверьте подключение к провайдеру. Остальные настройки уже подготовлены.",
    ) +
    `<form id="connect-form" class="panel setup-panel"><div class="eyebrow">ШАГ 1 ИЗ 1</div><h2>Подключение к интернету</h2><p>Выбрано автоматически по маршруту роутера. При необходимости выберите другое подключение.</p><div class="field"><label for="connect-wan">Подключение провайдера</label><select id="connect-wan" required><option value="">Выберите подключение</option>${networkInfo.interfaces.map((i) => `<option value="${esc(i.name)}" ${i.name === selected ? "selected" : ""}>${esc(i.name)}${i.addresses.length ? " — " + esc(i.addresses.join(", ")) : ""}</option>`).join("")}</select></div><label class="check-label"><input id="connect-tune" type="checkbox" checked> Сразу подобрать стратегию YouTube</label><div class="info-note">На время подбора возможны краткие перебои соединений. Discord можно подобрать отдельно в разделе «Сервисы и домены».</div><div class="form-actions"><button class="primary" type="submit">Сохранить и включить обход ↗</button></div><p id="connect-error" class="error" role="alert"></p></form>`
  );
}
async function refresh(render = false) {
  state = await api("status");
  updateChrome();
  updateServiceButtons();
  updateCheckReports();
  if (render) renderPage();
  else if (page === "overview") {
    $("#content").innerHTML = overview();
    bindActions();
    loadTelemetry();loadOverviewBoxes();
  } else if (page === "tune") {
    $("#job-panel").innerHTML = jobPanel();
    bindTrials();
    $("#tune-start").disabled = !state.running || state.busy;
    $("#tune-cancel").disabled = !state.job || !!state.job.finished;
  } else if (page === "device") { await loadDevice();
  } else if (page === "updates") { await loadUpdates();
  } else if (page === "adaptive") {
    await loadAdaptive();
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
            : page === "device" ? devicePage()
            : page === "updates" ? updatesPage()
            : page === "strategies"
              ? strategyCatalog()
              : page === "adaptive"
                ? heading(
                    "Коробки обхода",
                    "Группы наблюдений, найденные методы и история их проверки.",
                  ) +
                  `<div class="adaptive-heading"><details><summary>Как работают коробки</summary><p>Сходные признаки помогают выбирать кандидатов. Каждое новое соединение проверяется отдельно; группа не означает опознанное устройство провайдера.</p><p>«Скачать диагностику» сохраняет полный снимок методов и истории в JSON, независимо от фильтров. Файл содержит домены и имя WAN-интерфейса; пароли, токены, журналы и контрольные URL не включаются. На роутере ничего не меняется.</p></details><div class="adaptive-actions"><button id="adaptive-download" title="JSON с доменами и историей; без паролей и токенов">Скачать диагностику</button><button id="adaptive-refresh">Обновить</button></div></div>${adaptiveToolbar()}<div id="adaptive-results">Загрузка…</div>`
                : page === "connect"
                  ? connectPage()
                  : heading(
                      "Журнал",
                      "События контроллера и движка. Последние 200 строк.",
                      '<button id="refresh-logs">Обновить ↻</button>',
                    ) +
                    '<div class="panel"><pre id="log-view" class="log-view">Загрузка…</pre></div>';
  bindActions();
  if(page==="overview"){loadTelemetry();loadOverviewBoxes();}
  if(page==="device")loadDevice();
  if(page==="updates")loadUpdates();
  if (page === "logs") loadLogs();
  if (page === "adaptive") loadAdaptive();
  if (page === "connect" && !networkInfo)
    api("network")
      .then((info) => {
        networkInfo = info;
        if (page === "connect") renderPage();
      })
      .catch((e) => notify(e.message, true));
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
  bindMethodViews();bindTrials();bindCheckActions();
  $("#method-search")?.addEventListener("input",e=>{methodQuery=e.target.value;$("#method-results").innerHTML=methodResults();bindMethodViews();});
  $("#method-family")?.addEventListener("change",e=>{methodFamily=e.target.value;$("#method-results").innerHTML=methodResults();bindMethodViews();});
  $("#update-check")?.addEventListener("click",async e=>{const button=e.currentTarget;button.disabled=true;try{await api("updates/check","POST",{});await loadUpdates(true);}catch(error){notify(error.message,true);button.disabled=false;}});
  $("#update-install")?.addEventListener("click",async e=>{const button=e.currentTarget;button.disabled=true;try{await api("updates/install","POST",{version:updatesData.latest});await loadUpdates(true);}catch(error){notify(error.message,true);button.disabled=false;}});
  $("#custom-service-form")?.addEventListener("submit",event=>{event.preventDefault();serviceAction(async()=>{
    const f=event.target,bytes=new Uint32Array(2);crypto.getRandomValues(bytes);const id="custom-"+[...bytes].map(v=>v.toString(16).padStart(8,"0")).join("");
    const url=f.elements.url.value.trim(),source=f.elements.source.value.trim(),domains=lines(f.elements.domains.value);
    if(!domains.length)domains.push(new URL(url).hostname.toLowerCase());
    const p={id,name:f.elements.name.value.trim(),strategy:"none",domains,targets:[{url,status:200,contains:f.elements.contains.value}],domain_source:source,auto_domains:Boolean(source&&f.elements.auto.checked)};
    await api("services/custom","POST",p);
    let message="Сервис добавлен";if(source){try{await api("services/"+id+"/domains","POST",{});}catch(e){message+="; домены пока не загружены: "+e.message;}}
    notify(message);f.reset();
  });});
  document.querySelectorAll("[data-domain-sync]").forEach(button=>button.addEventListener("click",()=>serviceAction(async()=>{
    const id=button.dataset.domainSync,c=structuredClone(state.config),draft=readProfileDraft(id);if(!draft.domain_source)throw new Error("Выберите источник доменов");c.profiles[c.profiles.findIndex(p=>p.id===id)]=draft;
    await api("config","PUT",c);profileDrafts.delete(id);await api("services/"+id+"/domains","POST",{});notify("Домены загружены и проверены");
  })));

  $("#adaptive-refresh")?.addEventListener("click", loadAdaptive);
  $("#adaptive-download")?.addEventListener("click", downloadDiagnostics);
  $("#adaptive-search")?.addEventListener("input", (event) => {
    adaptiveQuery = event.target.value;
    adaptiveRevealSaved = true;
    renderAdaptive();
  });
  $("#adaptive-filter")?.addEventListener("change", (event) => {
    adaptiveFilter = event.target.value;
    adaptiveRevealSaved = true;
    renderAdaptive();
  });
  $("#catalog-update")?.addEventListener("click", () =>
    action(async () => {
      await api("catalog/update", "POST", {});
      notify(
        "Каталог загружен и проверен. Запустите подбор для применения новых вариантов.",
      );
    }),
  );
  $("#connect-form")?.addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = event.currentTarget.querySelector("button");
    button.disabled = true;
    const tune = $("#connect-tune").checked;
    try {
      const c = structuredClone(state.config);
      c.wan = $("#connect-wan").value;
      await api("config", "PUT", c);
      await api("service", "POST", { enabled: true });
      if (tune && c.profiles.some((p) => p.id === "youtube"))
        await api("tune", "POST", { profile: "youtube" });
      await refresh();
      location.hash = tune ? "tune" : "overview";
      notify("Обход включён");
    } catch (e) {
      $("#connect-error").textContent = e.message;
      button.disabled = false;
    }
  });
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
  document.querySelectorAll("[data-save-profile]").forEach((button) => button.addEventListener("click", () => serviceAction(async () => {
    const id = button.dataset.saveProfile, c = structuredClone(state.config);
    const submitted = readProfileDraft(id);
    c.profiles[c.profiles.findIndex((p) => p.id === id)] = submitted;
    await api("config", "PUT", c);
    if (JSON.stringify(profileDrafts.get(id) || submitted) === JSON.stringify(submitted)) profileDrafts.delete(id);
    notify("Профиль сохранён");
  })));
  document.querySelectorAll("[data-remove-service]").forEach((button) => button.addEventListener("click", () => serviceAction(async () => {
    const id = button.dataset.removeService;
    await api("services/" + encodeURIComponent(id), "DELETE");
    profileDrafts.delete(id); profileOpen.delete(id);
    if (selectedTuneProfile === id) selectedTuneProfile = "";
    notify("Профиль удалён; ручные правила и обучение сохранены");
  })));
  document.querySelectorAll("[data-profile-card]").forEach((card) => {
    const id = card.dataset.profileCard;
    card.addEventListener("toggle", () => profileOpen.set(id, card.open));
    card.addEventListener("input", () => profileDrafts.set(id, readProfileDraft(id)));
    card.addEventListener("change", () => profileDrafts.set(id, readProfileDraft(id)));
  });
  $("#service-catalog")?.addEventListener("toggle", (event) => { serviceCatalogOpen = event.currentTarget.open; });
  $("#service-search")?.addEventListener("input", (event) => {
    serviceCatalogQuery = event.target.value;
    $("#service-preset-list").innerHTML = presetRows();
    bindPresetActions($("#service-preset-list"));
  });
  $("#tune-profile")?.addEventListener("change", (event) => {
    selectedTuneProfile = event.target.value;
    $("#tune-profile-note").textContent = presetNote(selectedTuneProfile);
  });
  bindPresetActions(document);
  updateServiceButtons();
  $("#settings-form")?.addEventListener("submit", (event) => {
    event.preventDefault();
    action(async () => {
      const c = structuredClone(state.config);
      c.wan = $("#wan").value.trim();
      [
        "ipv6",
        "ppe",
        "quic",
        "discord_voice",
        "auto",
        "auto_catalog",
        "adaptive",
        "adaptive_reuse",
        "probe_load_guard",
      ].forEach((key) => (c[key] = $(`#${key}`).checked));
      c.adaptive_global_max_switches = Number($("#adaptive-global-max").value);
      c.adaptive_cooldown_seconds = Number($("#adaptive-cooldown").value);
      c.adaptive_max_switches = Number($("#adaptive-max-switches").value);
      c.interval_minutes = Number($("#interval").value);
      c.timeout_seconds = Number($("#timeout").value);
      c.attempts = Number($("#attempts").value);
      c.exclusions = lines($("#exclusions").value);
      await api("config", "PUT", c);
      notify("Настройки сохранены");
    });
  });
}
const symptomLabels = {
  rst: "Ранний сброс соединения",
  retransmission: "Повторные передачи",
  tls_alert: "Ранний TLS alert",
  unknown: "Признак пока не определён",
};
const originLabels = {
  initial: "Начальный метод",
  pool: "Из общего набора",
  learned: "По опыту коробки",
  restored: "Сохранённый метод",
  manual: "Выбран вручную",
};
const tlsLabels = {
  tls12: "TLS 1.2",
  tls13: "TLS 1.3",
  legacy: "Другой TLS",
  unknown: "TLS не определён",
};
function observedStatus(s) {
  if (s === "held") return "Смена отложена";
  return s === "connected"
    ? "Есть ответ"
    : s === "failed"
      ? "Неудача"
      : s === "selected"
        ? "Выбран для проверки"
        : "Пробуется";
}
function eventReason(r) {
  if (r.status === "held")
    return (
      (r.hold_reason === "limit"
        ? "Лимит смен домена или движка за 10 минут"
        : "Пауза между сменами") +
      " · до " +
      new Date(r.hold_until * 1000).toLocaleTimeString("ru-RU")
    );
  if (r.origin === "manual") return "Метод выбран вручную";
  if (r.status === "connected") return "Наблюдалась передача данных";
  return symptomLabels[r.symptom] || symptomLabels.unknown;
}
function currentHold(r, enabled = true, now = Date.now() / 1000) {
  if (!enabled || r.paused || r.pinned || !(r.hold_until > now)) return null;
  if (r.hold_reason !== "limit" && r.hold_reason !== "cooldown") return null;
  return {
    reason: r.hold_reason,
    label: r.hold_reason === "limit" ? "Лимит смен домена или движка за 10 минут" : "Пауза между сменами",
    until: r.hold_until,
  };
}
const observationFreshSeconds = 24 * 60 * 60;
function observationStale(r, now = Date.now() / 1000) {
  return !(r.updated > 0) || now - r.updated >= observationFreshSeconds;
}
function observationHasContext(r) {
  const wan = state?.config?.wan;
  return Boolean(r.network && r.network !== "-" && r.port > 0 && (!wan || r.network === wan));
}
function boxHasRecentTraffic(b) {
  return [...b.hosts, ...b.history].some((r) => observationHasContext(r) && !observationStale(r));
}
// Aggregate repeated outcomes only until the next state transition in this
// domain/group/context. Other domains can be interleaved without breaking a run.
function groupedHistory(rows) {
  const groups = [], lastByContext = new Map();
  rows.forEach((r, index) => {
    const context = JSON.stringify([r.host, r.group, r.network, r.port]);
    const signature = JSON.stringify([r.strategy, r.status, r.symptom, r.origin,
      r.status === "held" ? r.hold_reason : null,
      r.status === "held" ? r.hold_until : null]);
    let group = lastByContext.get(context);
    if (group?.signature === signature) {
      group.row = { ...r, event_count: group.row.event_count + (r.event_count || 1), first_updated: group.row.first_updated };
      group.index = index;
    } else {
      group = { signature, index, row: { ...r, event_count: r.event_count || 1, first_updated: r.first_updated ?? r.updated } };
      groups.push(group);
      lastByContext.set(context, group);
    }
  });
  return groups.sort((a, b) => b.index - a.index).map((g) => g.row);
}
function currentObservationStatus(r, enabled = true) {
  if (r.paused) return "Приостановлен";
  if (r.pinned && r.pinned !== r.strategy) return "Ожидает наблюдений";
  if (currentHold(r, enabled)) return "Смена отложена";
  if (observationStale(r)) return "Нет свежих наблюдений";
  if (!observationHasContext(r)) return "Сохранённое наблюдение";
  return observedStatus(r.status);
}
function currentObservationReason(r, enabled = true) {
  if (r.paused) return "Действует ручное исключение";
  if (r.pinned && r.pinned !== r.strategy) return "Для закреплённого метода ещё нет наблюдений";
  const hold = currentHold(r, enabled);
  if (hold) return `${hold.label} · до ${new Date(hold.until * 1000).toLocaleString("ru-RU")}`;
  if (observationStale(r)) return "За последние сутки нет записанных наблюдений; метод сохранён.";
  if (!observationHasContext(r)) return "Наблюдение относится к прежнему или неполному контексту подключения";
  return eventReason(r);
}
function observationTone(r, history, enabled) {
  if (!history) {
    if (r.paused || currentHold(r, enabled) || (r.pinned && r.pinned !== r.strategy)) return "warn";
    if (observationStale(r) || !observationHasContext(r)) return "neutral";
  }
  return r.status === "failed" ? "fail" : r.status === "connected" ? "" : "warn";
}
function observationTable(rows, history = false, enabled = true) {
  if (!rows.length) return '<p class="subtle">Наблюдений пока нет.</p>';
  return `<table class="probe-table"><thead><tr><th>Сайт / метод</th><th>Результат</th><th>Причина / источник</th><th>Время</th></tr></thead><tbody>${rows.map((r) => `<tr><td>${esc(r.host)}<br><small>${esc(strategyName(history ? r.strategy : r.pinned || r.strategy))}</small></td><td>${badge(history ? observedStatus(r.status) : currentObservationStatus(r, enabled), observationTone(r, history, enabled))}${history && r.event_count > 1 ? `<br><small>Событий: ${r.event_count}</small>` : ""}${!history && r.pinned ? '<br><small>Закреплён вручную</small>' : ""}</td><td>${esc(history ? eventReason(r) : currentObservationReason(r, enabled))}<br><small>${esc(originLabels[r.origin] || originLabels.restored)}</small></td><td>${history && r.event_count > 1 && r.first_updated !== r.updated ? `${esc(new Date(r.first_updated * 1000).toLocaleString("ru-RU"))}<br>— ` : ""}${esc(new Date(r.updated * 1000).toLocaleString("ru-RU"))}</td></tr>`).join("")}</tbody></table>`;
}
function controlScope(b) {
  return {
    tls: tlsLabels[b.tls] ? b.tls : "any",
    family: ["ipv4", "ipv6"].includes(b.family) ? b.family : "any",
    port: b.port || 0,
  };
}
function scopeText(p) {
  return `${tlsLabels[p.tls] || "Любой TLS"} · ${p.family === "ipv4" ? "IPv4" : p.family === "ipv6" ? "IPv6" : "Любой IP"} · ${p.port || "обрабатываемые порты"}`;
}
function matchingPin(host, scope) {
  return (state.config.domain_pins || []).find(
    (p) =>
      p.host === host &&
      (p.tls === "any" || p.tls === scope.tls) &&
      (p.family === "any" || p.family === scope.family) &&
      (!p.port || p.port === scope.port),
  );
}
function controlData(host, scope) {
  return `data-host="${esc(host)}" data-tls="${esc(scope.tls)}" data-family="${esc(scope.family)}" data-port="${scope.port}"`;
}
function domainControls(b, domains) {
  const scope = controlScope(b);
  return `<details class="domain-management" data-view-key="${esc(b.id)}/controls"><summary>Управление доменами</summary><p class="subtle">Закрепление: ${esc(scopeText(scope))}. Пауза охватывает домен и поддомены. Сброс удаляет опыт только выбранного имени во всех контекстах.</p>${domains
    .map((host) => {
      const pin = matchingPin(host, scope),
        ownPause = (state.config.paused_domains || []).includes(host),
        excluded = [
          ...state.config.exclusions,
          ...(state.config.paused_domains || []),
        ].find((d) => host === d || host.endsWith("." + d));
      const current =
        pin?.strategy ||
        b.hosts.find((r) => r.host === host)?.strategy ||
        "none";
      return `<form class="domain-control" ${controlData(host, pin || scope)}><strong>${esc(host)}</strong><label>Метод для закрепления<select name="strategy">${strategyOptions(current)}</select></label><div class="domain-buttons"><button type="submit" ${state.busy ? "disabled" : ""}>${pin ? "Изменить закрепление" : "Закрепить метод"}</button>${pin ? `<button type="button" data-domain-action="unpin" ${controlData(host, pin)}>Снять закрепление</button>` : ""}${ownPause ? '<button type="button" data-domain-action="resume">Возобновить обработку</button>' : !excluded ? '<button type="button" data-domain-action="pause">Приостановить</button>' : ""}<button type="button" data-domain-action="reset">Сбросить обучение</button></div>${excluded ? `<small>Обработка приостановлена правилом ${esc(excluded)}. ${!ownPause ? "Правила исключения — в настройках." : ""}</small>` : ""}</form>`;
    })
    .join("")}</details>`;
}
function manualRules() {
  const pins = state.config.domain_pins || [],
    paused = state.config.paused_domains || [];
  return `<details class="panel adaptive-rules" data-view-key="manual-rules"><summary>Ручные правила · ${pins.length} закреплений · ${paused.length} пауз</summary><p>Изменения правил и сброс обучения перезапускают работающий движок; возможен краткий перерыв. Закрепления действуют и при выключенном автоподборе по трафику.</p>${pins.map((p) => `<div class="manual-rule"><div><strong>${esc(p.host)}</strong><small>${esc(strategyName(p.strategy))} · ${esc(scopeText(p))}</small></div><button type="button" data-domain-action="unpin" ${controlData(p.host, p)}>Снять закрепление</button></div>`).join("")}${paused.map((host) => `<div class="manual-rule"><div><strong>${esc(host)}</strong><small>Пауза обработки по имени, включая поддомены</small></div><button type="button" data-domain-action="resume" data-host="${esc(host)}">Возобновить</button></div>`).join("")}${!pins.length && !paused.length ? '<p class="subtle">Выберите «Управление доменами» в карточке коробки, чтобы добавить правило.</p>' : ""}</details>`;
}
function bindDomainControls(root) {
  const submit = (button, data) =>
    action(async () => {
      button.disabled = true;
      try {
        await api("adaptive/domain", "POST", data);
        for (const key of adaptiveDrafts.keys()) {
          if (JSON.parse(key).host === data.host) adaptiveDrafts.delete(key);
        }
        notify(
          data.action === "reset"
            ? "Обучение домена сброшено; ручные правила сохранены"
            : "Правило домена сохранено",
        );
      } finally {
        button.disabled = false;
      }
    });
  root.querySelectorAll(".domain-control").forEach((form) =>
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const data = {
        ...form.dataset,
        port: Number(form.dataset.port),
        action: "pin",
        strategy: form.elements.strategy.value,
      };
      submit(form.querySelector("[type=submit]"), data);
    }),
  );
  root.querySelectorAll("[data-domain-action]").forEach((button) =>
    button.addEventListener("click", () => {
      const source = button.dataset.host
        ? button
        : button.closest(".domain-control");
      const data = {
        host: source.dataset.host,
        action: button.dataset.domainAction,
      };
      if (data.action === "unpin") {
        data.tls = source.dataset.tls;
        data.family = source.dataset.family;
        data.port = Number(source.dataset.port);
      }
      submit(button, data);
    }),
  );
}
function currentBoxMethods(rows, enabled = true, compact = false) {
  if (!rows.length)
    return '<p class="subtle">В этой коробке осталась только история. Текущих наблюдений нет.</p>';
  const groups = new Map();
  for (const r of rows) {
    const method = r.pinned || r.strategy;
    const status = currentObservationStatus(r, enabled);
    const hold = currentHold(r, enabled);
    const key = JSON.stringify([method, status, Boolean(r.pinned), hold?.reason]);
    const item = groups.get(key) || {
      method, status, pinned: Boolean(r.pinned), count: 0,
      hold, earliest: hold?.until, latest: hold?.until,
    };
    if (hold) {
      item.earliest = Math.min(item.earliest, hold.until);
      item.latest = Math.max(item.latest, hold.until);
    }
    item.count++;
    groups.set(key, item);
  }
  const priority = { "Смена отложена": 0, "Ожидает наблюдений": 1, "Пробуется": 2,
    "Выбран для проверки": 2, "Есть ответ": 3, "Приостановлен": 4,
    "Сохранённое наблюдение": 5, "Нет свежих наблюдений": 6 };
  const methods = [...groups.values()].sort((a, b) => (priority[a.status] ?? 7) - (priority[b.status] ?? 7));
  return methods.slice(0, compact ? 2 : methods.length).map((m) =>
    `<div class="box-method"><div class="box-method-line"><strong>${esc(m.method === "none" ? "Без вмешательства" : strategyName(m.method))}</strong>${badge(m.status, m.status === "Есть ответ" ? "" : "neutral")}</div><small>Доменов: ${m.count}${m.pinned ? " · закреплено вручную" : ""}</small>${m.hold ? `<small>${esc(m.hold.label)}<br>Снятие ограничения: ${esc(new Date(m.earliest * 1000).toLocaleString("ru-RU"))}${m.latest !== m.earliest ? " — " + esc(new Date(m.latest * 1000).toLocaleString("ru-RU")) : ""}</small>` : ""}</div>`
  ).join("") + (compact && methods.length > 2 ? `<small class="subtle">Остальные методы и состояния — в разделе «Домены и методы».</small>` : "");
}
function boxExperienceEmpty(b) {
  if (!b.hosts.length) return "Текущих наблюдений нет; прежние попытки доступны в истории.";
  const active = b.hosts.filter((r) => !r.paused);
  if (!active.length) return "Обработка доменов приостановлена ручными правилами.";
  if (active.every((r) => (r.pinned || r.strategy) === "none" && r.status === "connected"))
    return "Наблюдался ответ без вмешательства. Такой метод не пополняет опыт обхода.";
  return "Методов обхода с наблюдаемым ответом пока нет. Текущие методы показаны выше; для проверки нужны новые подключения к сайту.";
}
function boxCard(b, enabled = true) {
  const domains = [
    ...new Set([...b.hosts, ...b.history].map((r) => r.host)),
  ].sort();
  const history = groupedHistory(b.history);
  const eventTotal = b.history.reduce((sum, r) => sum + (r.event_count || 1), 0);
  return `<article class="panel bypass-box" data-box="${esc(b.id)}">
    <div class="panel-head"><h2>${esc(tlsLabels[b.tls] || tlsLabels.unknown)} · ${esc(b.family === "ipv6" ? "IPv6" : b.family === "ipv4" ? "IPv4" : "IP не определён")}</h2><span class="subtle">Домены: ${domains.length}</span></div>
    <p class="box-health">${esc(boxHealthLabel(b,enabled))} · смены в записанных 10-минутных окнах: ${boxHealth(b,enabled).switches}</p>
    <p class="box-context">${esc(symptomLabels[b.symptom] || symptomLabels.unknown)} · ${esc(b.network === "-" ? "WAN не записан" : b.network)}${b.port ? ":" + b.port : ""}</p>
    <p class="box-domains">${domains.slice(0, 2).map((d) => `<span title="${esc(d)}">${esc(d)}</span>`).join("")}${domains.length > 2 ? `<small>ещё ${domains.length - 2}</small>` : ""}</p>
    <div class="box-methods box-current" aria-label="Текущие методы доменов">${currentBoxMethods(b.hosts, enabled, true)}</div>
    <details data-view-key="${esc(b.id)}/observations"><summary>Домены и методы · ${domains.length}</summary>${currentBoxMethods(b.hosts, enabled)}${observationTable(b.hosts, false, enabled)}</details>
    <details data-view-key="${esc(b.id)}/experience"><summary>Опыт обхода · ${b.methods.length}</summary><div class="box-methods">${b.methods.length ? b.methods.map((m) => `<div class="box-method"><div class="box-method-line"><strong>${esc(m.name)}</strong>${badge(m.reusable ? "Кандидат по опыту" : "Сохранённое наблюдение", m.reusable ? "" : "neutral")}</div><small>${esc(familyLabels[m.family] || familyLabels.other)} · ответ на ${m.hosts} доменах</small></div>`).join("") : `<p class="subtle">${esc(boxExperienceEmpty(b))}</p>`}</div></details>
    ${domainControls(b, domains)}
    <details data-view-key="${esc(b.id)}/history"><summary>История · ${history.length} строк · событий: ${eventTotal}</summary>${observationTable(history, true)}</details>
    <small class="box-updated">Наблюдение: ${esc(new Date(b.updated * 1000).toLocaleString("ru-RU"))}</small>
  </article>`;
}
async function downloadDiagnostics(event) {
  const button = event.currentTarget;
  button.disabled = true;
  try {
    const report = await api("diagnostics");
    const blob = new Blob([JSON.stringify(report, null, 2) + "\n"], { type: "application/json" });
    const url = URL.createObjectURL(blob), link = document.createElement("a");
    link.href = url;
    link.download = "keenetic-pilot-diagnostics-" + new Date(report.generated_at).toISOString().replace(/[:.]/g, "-") + ".json";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    notify("Диагностика подготовлена. Файл содержит домены и историю соединений.");
  } catch (error) {
    notify("Не удалось скачать диагностику: " + error.message, true);
  } finally {
    button.disabled = false;
  }
}
let adaptiveData = null,
  adaptiveQuery = "",
  adaptiveFilter = "all",
  adaptiveLoading = false,
  adaptiveRevealSaved = false;
const adaptiveDetails = new Map(), adaptiveDrafts = new Map();
function adaptiveToolbar() {
  const filters = { all: "Все состояния", stale: "Нет свежих наблюдений", probing: "Пробуется", held: "Смена отложена", connected: "Есть ответ", pinned: "Закреплён", paused: "Приостановлен" };
  return `<div class="panel adaptive-toolbar"><label>Поиск по домену<input type="search" id="adaptive-search" placeholder="Например: youtube.com" value="${esc(adaptiveQuery)}"></label><label>Состояние домена<select id="adaptive-filter">${Object.entries(filters).map(([id, name]) => `<option value="${id}" ${id === adaptiveFilter ? "selected" : ""}>${name}</option>`).join("")}</select></label><p id="adaptive-update" class="subtle" role="status">Обновление каждые 4 секунды, пока страница открыта.</p></div>`;
}
function boxMatches(b, query, filter, enabled) {
  const matchesDomain = (r) => r.host.toLowerCase().includes(query.trim().toLowerCase());
  if (filter === "all") return [...b.hosts, ...b.history].some(matchesDomain);
  return b.hosts.some((r) => {
    if (!matchesDomain(r)) return false;
    if (filter === "stale") return observationStale(r);
    if (filter === "pinned") return Boolean(r.pinned);
    if (filter === "paused") return r.paused;
    if (filter === "held") return Boolean(currentHold(r, enabled));
    if (filter === "connected") return currentObservationStatus(r, enabled) === "Есть ответ";
    return filter === "probing" && ["Пробуется", "Выбран для проверки", "Ожидает наблюдений"].includes(currentObservationStatus(r, enabled));
  });
}
function adaptiveFormKey(form) {
  return JSON.stringify({ box: form.closest("[data-box]").dataset.box, ...form.dataset });
}
function saveAdaptiveView(target) {
  target.querySelectorAll("details[data-view-key]").forEach((detail) => {
    adaptiveDetails.set(detail.dataset.viewKey, detail.open);
  });
}
function renderAdaptive() {
  const target = $("#adaptive-results");
  if (!target || !adaptiveData) return;
  // Keep native selects and buttons in place while the user interacts with them.
  if (target.querySelector(".domain-control:focus-within")) {
    $("#adaptive-update").textContent = "Новые данные будут показаны после выхода из управления доменом.";
    return;
  }
  saveAdaptiveView(target);
  const data = adaptiveData, boxes = data.boxes || [];
  const visible = boxes.filter((b) => boxMatches(b, adaptiveQuery, adaptiveFilter, data.enabled));
  const recent = visible.filter(boxHasRecentTraffic).sort(problemFirst);
  const saved = visible.filter((b) => !boxHasRecentTraffic(b));
  if (adaptiveRevealSaved && (saved.length || (!adaptiveQuery.trim() && adaptiveFilter === "all"))) {
    if (saved.length && (adaptiveQuery.trim() || adaptiveFilter !== "all")) adaptiveDetails.set("saved-boxes", true);
    adaptiveRevealSaved = false;
  }
  target.innerHTML =
    loadControlPanel() + manualRules() +
    (!data.enabled
      ? '<div class="notice">Включите «Автоподбор по трафику» в настройках. Текущие стратегии сохранятся.</div>'
      : '<p class="adaptive-note">«Есть ответ» — передача данных, без подтверждения видео и голоса. Свежие наблюдения — за последние сутки. Отсутствие свежих записей не останавливает автоподбор: он продолжится при новых сбоях трафика.</p>') +
    (data.error ? `<p class="fail-text">${esc(data.error)}</p>` : "") +
    `<div class="box-summary"><span>Показано коробок: <b>${visible.length} из ${boxes.length}</b></span><span>Обнаружено новых доменов: <b>${data.discovered.length}</b></span><span>Использование опыта: <b>${data.reuse ? "включено" : "выключено"}</b></span></div>` +
    (visible.length
      ? `${recent.length ? `<div class="box-grid">${recent.map((b) => boxCard(b, data.enabled)).join("")}</div>` : '<p class="subtle">Свежих наблюдений с актуальным контекстом подключения нет.</p>'}${saved.length ? `<details class="panel saved-boxes" data-view-key="saved-boxes"><summary>Сохранённый опыт и история · коробок: ${saved.length}</summary><p class="subtle">Здесь нет свежих записей с текущим WAN и известным портом. Методы сохранены; новые результаты наблюдений снова появятся в основном списке. Этот раздел ничего не отключает.</p><div class="box-grid">${saved.map((b) => boxCard(b, data.enabled)).join("")}</div></details>` : ""}`
      : boxes.length
        ? '<div class="panel empty"><h3>Совпадений нет</h3><p>Измените домен или выберите другое состояние. Поиск и состояние должны совпасть у одного домена. Карточка показывает весь контекст коробки.</p></div>'
        : '<div class="panel empty"><h3>Коробки пока не сформированы</h3><p>Включите автоподбор по трафику и откройте нужный сайт. Наблюдения появятся после ошибок или ответов настроенных сервисов. Прежние сохранённые методы продолжают действовать.</p></div>');
  target.querySelectorAll("details[data-view-key]").forEach((detail) => {
    const key = detail.dataset.viewKey;
    detail.open = adaptiveDetails.get(key) || false;
    detail.addEventListener("toggle", () => adaptiveDetails.set(key, detail.open));
  });
  target.querySelectorAll(".domain-control").forEach((form) => {
    const key = adaptiveFormKey(form), select = form.elements.strategy;
    const draft = adaptiveDrafts.get(key);
    if (draft && [...select.options].some((option) => option.value === draft)) select.value = draft;
    select.addEventListener("change", () => adaptiveDrafts.set(key, select.value));
    form.addEventListener("focusout", () => setTimeout(renderAdaptive, 0));
  });
  bindDomainControls(target);
  $("#adaptive-update").textContent = "Обновлено: " + new Date(data.receivedAt).toLocaleTimeString("ru-RU") + ". Автообновление каждые 4 секунды.";
}
async function loadAdaptive() {
  const target = $("#adaptive-results");
  if (!target || adaptiveLoading) return;
  adaptiveLoading = true;
  try {
    const data = await api("adaptive");
    // A response from a previous page must not overwrite a newly opened view.
    if (target !== $("#adaptive-results") || !signedIn) return;
    adaptiveData = { ...data, receivedAt: Date.now() };
    renderAdaptive();
  } catch (e) {
    if (target === $("#adaptive-results"))
      $("#adaptive-update").textContent = "Не удалось обновить наблюдения: " + e.message + ". Показаны последние полученные данные.";
  } finally {
    adaptiveLoading = false;
  }
}
const cpuSamples=[];
let telemetryLoading=false,overviewBoxesLoading=false;
function cpuChart(){const values=cpuSamples.filter(v=>v.value!=null);if(values.length<2)return '<p class="subtle">График появится после двух замеров CPU.</p>';const now=Date.now(),start=now-60000,points=values.map(v=>`${Math.max(0,Math.min(300,(v.at-start)/200))},${Math.max(0,Math.min(70,70-v.value*.7))}`).join(" ");return `<svg class="cpu-chart" viewBox="0 0 300 90" role="img" aria-label="Общая загрузка CPU за последние 60 секунд"><path d="M0 0V70H300M0 35H300" class="chart-grid"/><polyline points="${points}"/><text x="0" y="87">−60 с</text><text x="240" y="87">сейчас</text></svg>`;}
function telemetryMarkup(d){return `<div class="telemetry-cell"><span>CPU</span><strong>${d.cpu_percent==null?"—":d.cpu_percent.toFixed(1)+" %"}</strong><small>${d.cpu_percent==null?"Нет замера CPU":"Общая загрузка роутера"}</small></div><div class="telemetry-cell"><span>Память</span><strong>${d.memory_available==null?"—":prettyBytes(d.memory_total-d.memory_available)}</strong><small>из ${prettyBytes(d.memory_total||null)}</small>${d.memory_available!=null&&d.memory_total?`<meter min="0" max="${d.memory_total}" value="${d.memory_total-d.memory_available}" aria-label="Занятая память"></meter>`:""}</div><div class="telemetry-cell"><span>Температура</span>${d.temperatures.length?d.temperatures.map(t=>`<div class="sensor-value"><small>${esc(t.name)}</small><strong>${t.celsius.toFixed(1)} °C</strong></div>`).join(""):'<strong>—</strong><small>Датчики не предоставлены</small>'}</div><div class="telemetry-cell"><span>WAN · ${esc(d.wan||"не задан")}</span><strong>↓ ${prettyRate(d.rx_bytes_per_second)}</strong><strong>↑ ${prettyRate(d.tx_bytes_per_second)}</strong><small>Текущая скорость</small></div>`;}
function prettyRate(bytes){return bytes==null?"—":(bytes*8/1000000).toLocaleString("ru-RU",{maximumFractionDigits:1})+" Мбит/с";}
async function loadTelemetry(){const target=$("#network-telemetry");if(!target||telemetryLoading)return;telemetryLoading=true;try{const d=await api("device");if(!signedIn||target!==$("#network-telemetry"))return;deviceData=d;const now=Date.now();cpuSamples.push({at:now,value:d.cpu_percent});while(cpuSamples.length>16||cpuSamples[0]?.at<now-60000)cpuSamples.shift();target.innerHTML=telemetryMarkup(d);const chart=$("#cpu-chart");if(chart)chart.innerHTML=cpuChart();}catch(e){if(target===$("#network-telemetry"))target.textContent="Показатели недоступны: "+e.message;}finally{telemetryLoading=false;}}
function boxHealth(b,enabled=true){const stats={connected:0,probing:0,held:0,paused:0,pinned:0,stale:0,switches:0};const now=Date.now()/1000;for(const r of b.hosts){if(r.window_start>0&&r.window_start<=now&&r.window_start+600>now)stats.switches+=r.switches||0;if(r.paused)stats.paused++;else if(observationStale(r))stats.stale++;else if(currentHold(r,enabled))stats.held++;else if(r.status==="connected")stats.connected++;else if(r.pinned)stats.pinned++;else stats.probing++;}return stats;}
function boxHealthLabel(b,enabled){if(!enabled)return `Автоподбор выключен · сохранено наблюдений: ${b.hosts.length}`;const v=boxHealth(b,enabled);return `Ответ: ${v.connected} · проверяются: ${v.probing} · ждут: ${v.held} · пауза: ${v.paused} · закреплены: ${v.pinned} · старые: ${v.stale}`;}
function problemFirst(a,b){const av=boxHealth(a),bv=boxHealth(b);return (bv.probing+bv.held)-(av.probing+av.held)||b.updated-a.updated;}
async function loadOverviewBoxes(){const target=$("#overview-boxes");if(!target||overviewBoxesLoading)return;overviewBoxesLoading=true;try{const d=await api("adaptive");if(!signedIn||target!==$("#overview-boxes"))return;const boxes=d.boxes||[],recent=boxes.filter(boxHasRecentTraffic).sort(problemFirst),saved=boxes.length-recent.length;target.innerHTML=(recent.length?`<div class="table-scroll"><table class="box-overview-table"><thead><tr><th>Группа</th><th>Домены</th><th>Состояния</th></tr></thead><tbody>${recent.map(b=>`<tr><td><a href="#adaptive">${esc(tlsLabels[b.tls]||tlsLabels.unknown)} · ${esc(b.family)}</a></td><td>${new Set(b.hosts.map(r=>r.host)).size}</td><td>${esc(boxHealthLabel(b,d.enabled))}</td></tr>`).join("")}</tbody></table></div>`:'<p>Свежих наблюдений пока нет. При новых подключениях автоподбор продолжится.</p>')+`<small>Сохранённая история: ${saved} коробок. ${!d.enabled?"Автоподбор по трафику выключен.":""}</small>`;}catch(e){if(target===$("#overview-boxes"))target.textContent="Наблюдения недоступны: "+e.message;}finally{overviewBoxesLoading=false;}}
let deviceData=null,deviceLoading=false,updatesData=null,updatesLoading=false,updatesSerial=0;
const prettyBytes=n=>n==null?"недоступно":n>=1048576?(n/1048576).toFixed(1)+" МиБ":n>=1024?(n/1024).toFixed(1)+" КиБ":Math.round(n)+" Б";
function devicePage(){return heading("Роутер","Данные устройства, на котором запущен Pilot. Недоступные датчики не заменяются примерными значениями.")+`<div id="device-data">Загрузка…</div>`;}
async function loadDevice(){const target=$("#device-data");if(!target||deviceLoading)return;deviceLoading=true;try{const d=await api("device");if(target!==$("#device-data"))return;deviceData=d;target.innerHTML=`<div class="stats-grid"><div class="panel"><h3>Процессор</h3><strong>${d.cpu_percent==null?(d.cpu_sample_available?"ожидается второй замер":"недоступно"):d.cpu_percent.toFixed(1)+" %"}</strong><p>Ядер: ${d.cpu_cores}</p><small>Load 1/5/15 мин: ${d.load.length?d.load.map(v=>v.toFixed(2)).join(" / "):"недоступно"}</small></div><div class="panel"><h3>Память</h3><strong>${d.memory_available==null?"недоступно":prettyBytes(d.memory_total-d.memory_available)+" занято"}</strong><p>Всего: ${prettyBytes(d.memory_total||null)}</p><small>Доступно: ${prettyBytes(d.memory_available)}</small></div><div class="panel"><h3>Температура</h3>${d.temperatures.length?d.temperatures.map(t=>`<p><strong>${t.celsius.toFixed(1)} °C</strong> · ${esc(t.name)}</p>`).join(""):'<p>Недоступно: прошивка не предоставляет датчик Pilot.</p>'}</div></div><div class="panel"><h3>${esc(d.model||"Модель не предоставлена прошивкой")}</h3><p>${esc(d.platform)} · ядро ${esc(d.kernel||"неизвестно")}</p><p>Время работы: ${d.uptime_seconds==null?"недоступно":Math.floor(d.uptime_seconds/86400)+" д "+Math.floor(d.uptime_seconds%86400/3600)+" ч "+Math.floor(d.uptime_seconds%3600/60)+" мин"}</p><p>WAN ${esc(d.wan||"не задан")}: получено ${prettyBytes(d.rx_bytes)}, передано ${prettyBytes(d.tx_bytes)}</p><p>Сейчас: ↓ ${prettyBytes(d.rx_bytes_per_second)}/с · ↑ ${prettyBytes(d.tx_bytes_per_second)}/с</p></div>`;}catch(e){if(target===$("#device-data"))target.textContent="Не удалось получить данные: "+e.message;}finally{deviceLoading=false;}}
function updatesPage(){return heading("Обновления","Обновление Pilot, панели и закреплённого nfqws2 с сохранением настроек.")+`<div class="panel"><div id="update-info">Загрузка…</div><div class="form-actions"><button id="update-check">Проверить версию</button><button id="update-install" class="primary" disabled>Установить обновление</button></div><p class="subtle">Выпуск проверяется по подписи и SHA-256. Перед остановкой службы проверяются пакет и конфигурация; установщик сохраняет прежнюю версию для отката при ошибке. Будет краткий перерыв. После перезапуска войдите с прежним паролем.</p></div><div class="panel"><h3>Каталог методов</h3><p>Подписанные стратегии можно загрузить отдельно от приложения.</p><button id="catalog-update">Загрузить стратегии сейчас</button></div>`;}
async function loadUpdates(force=false){const target=$("#update-info");if(!target||(updatesLoading&&!force))return;updatesLoading=true;const serial=++updatesSerial;try{const d=await api("updates");if(target!==$("#update-info")||serial!==updatesSerial)return;updatesData=d;const stages={idle:"Готово к проверке",downloading:"Загрузка и проверка пакета",installing:"Установка и перезапуск службы",completed:"Обновление завершено",failed:"Обновление не завершено. Подробности — в локальном update.log; проверьте состояние службы.",interrupted:"Процесс обновления прерван; проверьте состояние службы перед повтором."};target.innerHTML=`<h3>Pilot ${esc(d.current)}</h3><p>nfqws2: ${esc(d.engine||"версия не получена")}</p><p>${esc(stages[d.stage]||d.stage)}</p><p>${d.latest?"Последний проверенный выпуск: "+esc(d.latest)+(d.latest_engine?" · nfqws2 "+esc(d.latest_engine):""):"Проверка новых выпусков ещё не выполнялась"}</p>${!d.supported?'<p class="subtle">Установка из панели доступна для управляемой установки на роутере.</p>':""}`;$("#update-install").disabled=!d.supported||!d.available||d.running||state.busy;$("#update-install").textContent=d.available?"Установить "+d.latest:"Установить обновление";$("#update-check").disabled=d.running||state.busy;}catch(e){if(target===$("#update-info"))target.textContent="Связь с панелью временно недоступна: "+e.message;}finally{if(serial===updatesSerial)updatesLoading=false;}}
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
  signedIn = true;
  if (token) sessionStorage.setItem("pilot-token", token);
  $("#login").classList.add("hidden");
  $("#shell").classList.remove("hidden");
  renderPage();
}
async function loadAuth() {
  const response = await fetch("/auth/status");
  if (!response.ok)
    throw new Error(
      "Не удалось связаться с панелью. Обновите страницу или проверьте службу.",
    );
  authInfo = await response.json();
  const setup = !authInfo.configured;
  $("#pairing-field").classList.toggle("hidden", !setup);
  $("#pairing-code").required = setup;
  $("#password").minLength = setup ? 10 : 1;
  $("#password").autocomplete = setup ? "new-password" : "current-password";
  $("#password-label").textContent = setup
    ? "Придумайте пароль панели"
    : "Пароль";
  $("#login-submit").textContent = setup
    ? "Создать пароль и открыть панель ↗"
    : "Войти в панель ↗";
  $("#login-hint").textContent = setup
    ? authInfo.pairing_available
      ? "Введите код, который показала установка, и задайте свой пароль."
      : "Код истёк или ещё не создан. В SSH выполните: /opt/keenetic-pilot/bin/pilot --pair"
    : "Введите пароль, который задали при первом входе.";
  return authInfo;
}
$("#login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  $("#login-submit").disabled = true;
  try {
    const response = await fetch(
      authInfo.configured ? "/auth/login" : "/auth/setup",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          code: $("#pairing-code").value.trim(),
          password: $("#password").value,
        }),
      },
    );
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "Вход не выполнен");
    authInfo.configured = true;
    token = "";
    sessionStorage.removeItem("pilot-token");
    await enter();
    $("#password").value = "";
    $("#pairing-code").value = "";
    $("#login-error").textContent = "";
  } catch (e) {
    $("#login-error").textContent = e.message;
  } finally {
    $("#login-submit").disabled = false;
  }
});
$("#logout").addEventListener("click", logout);
$("#logout-mobile").addEventListener("click", logout);
window.addEventListener("hashchange", () => {
  if (state && signedIn) renderPage();
});
loadAuth()
  .then((info) => {
    if (info.authenticated || token) return enter();
  })
  .catch((e) => {
    $("#login-error").textContent = e.message;
  });
setInterval(() => {
  if (signedIn && state && !document.hidden)
    refresh().catch((e) => notify(e.message, true));
}, 4000);
