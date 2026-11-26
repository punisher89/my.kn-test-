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
  geo: "Geo Routing",
};
let state,
  page = "overview",
  token = sessionStorage.getItem("pilot-token") || "",
  toastTimer;
document.addEventListener("visibilitychange",()=>document.documentElement.classList.toggle("page-hidden",document.hidden));
const panelNotifications=[];
let signedIn = false,
  authInfo = { configured: true },
  networkInfo;

const pollingEndpoints = new Set(["geo", "device", "adaptive", "preflight", "updates", "updates/sources"]);
async function api(path, method = "GET", data, signal) {
  const session = panelSession;
  const deadline = !signal && method === "GET" && pollingEndpoints.has(path) ? new AbortController() : null;
  const timeout = deadline ? setTimeout(() => deadline.abort(), 30000) : null;
  try {
    const response = await fetch(`/api/${path}`, {
      method,
      signal: signal || deadline?.signal,
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: data === undefined ? undefined : JSON.stringify(data),
    });
    if (!response.ok) {
      if (response.status === 401 && session === panelSession) logout();
      const body = await response.json().catch(() => ({}));
      const error = new Error(body.error || `HTTP ${response.status}`);
      error.status = response.status;
      throw error;
    }
    return await response.json();
  } finally {
    if (deadline) clearTimeout(timeout);
  }
}
function notify(message, error = false) {
  const msg=String(message).slice(0,500);
  if(signedIn&&(panelNotifications[0]?.message!==msg||panelNotifications[0]?.error!==error)){panelNotifications.unshift({message:msg,error,at:new Date().toLocaleTimeString("ru-RU")});if(panelNotifications.length>50)panelNotifications.pop();renderNotifications();}
  const toast = $("#toast");
  toast.textContent = message;
  toast.className = `toast${error ? " error" : ""}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.add("hidden"), 6000);
}
async function logout() {
  stopUpdateMonitor();
  panelNotifications.length=0;
  document.dispatchEvent(new Event("pilot-logout"));
  signedIn = false;
  panelSession++;
  refreshSerial++;
  cancelBatch();
  singleCheck?.abort();
  batchMessage = "";
  lastContact = 0;
  lostContact = false;
  $("#connection-banner").hidden = true;
  adaptiveData = null;
  adaptiveDrafts.clear();
  adaptiveDetails.clear();
  profileDrafts.clear();
  profileOpen.clear();
  checkOpen.clear();cpuSamples.length=0;deviceData=null;
  videoProbeReport=null; videoProbeBusy=false;
  serviceCatalogQuery = ""; serviceCatalogOpen = false; selectedTuneProfile = "";
  preflightCached="";preflightUpdated=0;
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
  split: "Разделение (Split)",
  disorder: "Изменение порядка (Disorder)",
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
        (s) => (s.family === "split" && s.actions?.some(a=>a.startsWith("multidisorder:")) ? "disorder" : (s.family || (s.id === "none" ? "direct" : "other"))) === id,
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
function strategyCatalog(){return heading("Каталог методов","Семейства и параметры раскрываются по нажатию. Наличие метода не гарантирует доступ.")+`<div class="panel"><strong>Всего вариантов: ${state.strategies.length}</strong><p class="subtle">Встроенные: ${state.strategies.filter(m=>!m.id.startsWith("net-")).length} · из загруженного каталога: ${state.strategies.filter(m=>m.id.startsWith("net-")).length}. Без вмешательства — контрольный вариант, не обход.</p><p>${methodGroups().map(g=>`${esc(g.name)}: ${g.methods.length}`).join(" · ")}</p></div><div class="panel method-toolbar"><label>Поиск<input id="method-search" type="search" value="${esc(methodQuery)}" placeholder="Название или семейство"></label><label>Семейство<select id="method-family"><option value="all">Все семейства (${state.strategies.length})</option>${methodGroups().map(g=>`<option value="${esc(g.id)}" ${methodFamily===g.id?"selected":""}>${esc(g.name)} (${g.methods.length})</option>`).join("")}</select></label></div><div class="method-list" id="method-results">${methodResults()}</div>`;}
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
function domainStageName(stage,reason){if(stage==="waiting")return ({resources:"Ждём свободные ресурсы",switch_budget:"Ждём лимит смен",time_slice:"Поиск продолжится"})[reason]||"Ожидание продолжения";return ({direct:"Проверка напрямую",search:"Испытание методов",confirm:"Подтверждение",applied:"Метод применён",unmeasured:"Недостаточно данных",exhausted:"Каталог проверен",waiting:"Поиск на паузе",cancelled:"Поиск остановлен",error:"Не удалось применить"})[stage]||stage;}
function domainReasonHelp(d){return ({
 client_hello_missing:"Открой этот сервис на проблемном устройстве. Pilot ждёт свежий образец ошибочного TLS-подключения и повторит проверку автоматически.",
 client_hello:"Форма TLS-подключения пока не позволяет завершить проверку. Открой сервис повторно на том же устройстве; поиск продолжится автоматически.",
 execution_unconfirmed:"Движок не подтвердил выполнение пробного метода. Pilot повторит этот кандидат автоматически; если причина повторяется, выгрузи диагностику.",
 resources:"Поиск ждёт снижения нагрузки роутера и продолжится автоматически.",
 http_response:"Сервер ответил, но контрольный адрес не прошёл проверку кода или содержимого. Для CDN может требоваться адрес конкретного ресурса; такой ответ не подтверждает работу сервиса.",
 dns:"Не удалось разрешить имя. Проверь DNS роутера; после паузы Pilot повторит проверку.",
 certificate:"Не прошла проверка сертификата. Проверь время на роутере и сертификат сайта; проверка TLS остаётся включённой.",
 probe_unmeasured:"Проба не дала достоверного результата. Подробности ниже; при повторении выгрузи диагностику.",
 video_url_required:"Для видеодомена Googlevideo общий HTTPS-ответ не подтверждает загрузку видео. Текущий метод сохранён. Проверьте фрагмент в разделе «Сервисы и домены».",
 mixed_direct:"Прямые подключения дали разные результаты. Pilot повторит проверку после паузы.",
 time_slice:"Порция поиска завершена. Следующая попытка продолжится с того же кандидата; другие домены тоже получат очередь.",
 switch_budget:"Достигнут лимит смен рабочего метода. Поиск продолжится после окончания паузы."
 })[d.reason]||"";}
function domainSearchMini(){const d=state.domain_job;return `<div class="panel domain-search-mini"><div><strong>Полный автоподбор ${state.config.adaptive&&state.config.adaptive_active?"включён":"выключен"}</strong><p>${d?(d.finished?"Последний поиск: ":"")+esc(d.scope.host)+" · "+esc(domainStageName(d.stage,d.reason))+(d.candidate&&!d.finished?" · "+esc(strategyName(d.candidate)):""):"После повторных ошибок Pilot сам проверит домен и испытает методы."}</p><small>В очереди: ${state.domain_queue||0} · сохранено HTTPS-подтверждений: ${state.verified_domains||0}</small></div><a class="button-link" href="#tune">Ход поиска →</a></div>`;}
function domainSearchPanel(){
 const d=state.domain_job;if(!d)return '<div class="panel"><h3>Полный автоподбор доменов</h3><p>После повторных сбоев запускает собственные HTTPS-пробы. Очередь: '+(state.domain_queue||0)+'. Пробные методы изолированы от обычного трафика.</p></div>';
 const key="domain/"+d.started;
 return `<div class="panel domain-search-report"><div class="panel-head"><h2>${d.finished?"Последний поиск":"Автоподбор"} · ${esc(d.scope.host)}</h2>${badge(domainStageName(d.stage,d.reason),d.stage==="applied"?"":"warn")}</div><p>${esc(tlsLabels[d.scope.tls]||d.scope.tls)} · ${esc(d.scope.family)} · порт ${d.scope.port} · в очереди: ${state.domain_queue||0}${d.candidate_total?" · кандидат "+d.candidate_index+"/"+d.candidate_total:""}</p><p>${d.candidate&&!d.finished?"Сейчас: "+esc(strategyName(d.candidate)):d.selected?"Применён: "+esc(strategyName(d.selected)):"Действующий метод сохранён"}</p>${domainReasonHelp(d)?`<p class="domain-search-help">${esc(domainReasonHelp(d))}</p>`:""}${state.background_hold?`<p class="domain-resource-hold">Фоновые проверки отложены: ${esc(state.background_hold)}</p>`:""}${d.error?`<p class="subtle">${esc(d.error)}</p>`:""}${d.next_at?`<p>Следующая попытка не раньше ${esc(new Date(d.next_at).toLocaleString("ru-RU"))}</p>`:""}<p class="subtle">Проверяется HTTPS с роутера; это не подтверждение видео, голоса или ClientHello телевизора.</p>${(d.trials||[]).map((t,i)=>`<details class="trial trial-compact" data-trial="${esc(key+"/"+i)}" ${trialOpen.get(key+"/"+i)?"open":""}><summary><strong>${esc(t.strategy.startsWith("direct:")?"Без обхода":(t.strategy.startsWith("confirm:")?"Подтверждение · ":"")+strategyName(t.strategy.split(":").slice(1).join(":")))}</strong><small>${t.results.filter(r=>r.ok).length}/${t.results.length} проб</small>${badge(t.passed?"Пройдено":t.error?"Не измерено":"Не прошло",t.passed?"":"warn")}</summary>${t.error?`<p>${esc(t.error)}</p>`:""}<div class="table-scroll"><table class="probe-table"><tbody>${t.results.map(r=>`<tr><td>${esc(r.url)}${r.peer?`<br><small>${esc(r.peer)}${r.client_hello_response?" · есть ответ на форму ClientHello":""}</small>`:""}</td><td>${esc(r.tls)} / ${esc(r.family)}</td><td>${r.status||"—"} · ${r.duration_ms} мс</td><td>${r.ok?"Есть ответ":esc(r.error)}</td></tr>`).join("")}</tbody></table></div></details>`).join("")}${d.finished?`<button id="domain-retry" ${manualBlocked()?"disabled":""}>Повторить поиск этого домена</button>`:""}</div>`;
}
function tuneRunning(){return Boolean((state.job&&!state.job.finished)||(state.domain_job&&!state.domain_job.finished));}
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
    `<div class="status-card"><div class="status-main"><span class="dot"></span><strong>${state.running?"ОБХОД ВКЛЮЧЁН":"ОБХОД ОСТАНОВЛЕН"}</strong><small>WAN · ${esc(c.wan||"не задан")}</small></div><button id="service-toggle" ${manualBlocked()?"disabled":""}>${state.running?"Остановить":"Запустить обход"}</button></div>
    ${!c.enabled&&!state.job?'<div class="panel welcome"><h2>Первый запуск</h2><a class="button-link primary" href="#connect">Настроить подключение</a></div>':""}
    <div id="network-telemetry" class="network-telemetry" aria-live="off">${deviceData?telemetryMarkup(deviceData):"Загрузка показателей роутера…"}</div>
    ${domainSearchMini()}<div class="dashboard-columns"><section class="panel"><div class="panel-head"><h2>Сервисы</h2><a href="#profiles">Управление →</a></div><div class="form-actions"><button id="check-all">Проверить все сервисы</button><button id="check-stop" hidden>Остановить проверку</button><small id="batch-status" role="status"></small></div><div class="service-check-list">${c.profiles.map(p=>`<div class="service-row">${logo(p)}<div class="service-meta"><h3>${esc(p.name)}</h3><span>${esc(strategyName(p.strategy))} · ${p.domains.length} доменов</span></div><div class="service-check-status" data-check-status="${esc(p.id)}">${checkStatus(p)}</div><button data-check-profile="${esc(p.id)}">Проверить</button></div><div data-check-report="${esc(p.id)}">${checkReport(p)}</div>`).join("")}</div><p class="info-note">Проверяются контрольные HTTPS-адреса сервиса. Ответ не подтверждает видео, звонки и работу всех доменов.</p></section><aside>${loadControlPanel()}<p class="subtle"><a href="#tune">Последний подбор: ${esc(jobSummary())} →</a></p></aside></div>
    <section class="panel"><div class="panel-head"><h2>Коробки обхода</h2><a href="#adaptive">Открыть →</a></div><div id="overview-boxes">Загрузка наблюдений…</div></section>`;
}
function loadControlPanel(){const c=state.config;return `<section class="panel load-control"><div class="panel-head"><h2>Контроль нагрузки</h2><a href="#settings">Настроить →</a></div><div id="cpu-chart">${cpuChart()}</div><div class="info-line"><span>Пауза между сменами</span><strong>${c.adaptive_cooldown_seconds} с</strong></div><div class="info-line"><span>Быстрый старт</span><strong>${c.adaptive_fast_start?"Первые 3 смены · 10 с":"Выключен"}</strong></div><div class="info-line"><span>Лимит на домен и контекст</span><strong>${c.adaptive_max_switches} / 10 мин</strong></div><div class="info-line"><span>Общий лимит движка</span><strong>${c.adaptive_global_max_switches??30} / 10 мин</strong></div><div class="info-line"><span>Защита фоновых проверок</span>${badge(c.probe_load_guard!==false?"Включена":"Выключена",c.probe_load_guard!==false?"":"warn")}</div><p>${state.background_hold?"Фоновые проверки отложены: "+esc(state.background_hold)+". Состояние ресурсов проверяется повторно.":"Проверки идут последовательно. Работающий обход продолжается при паузе фоновых задач."}</p><small>CPU и память общие для роутера. Коробки не являются отдельными процессами.</small></section>`;}
const checkPending=new Set(),checkOpen=new Map();
function checkStatus(p){const r=state.checks?.[p.id];if(checkPending.has(p.id))return badge("Проверяется","warn");if(!r)return badge("Не проверялся","neutral");return badge(r.stale?"Проверка устарела":!r.complete?"Не завершена":r.passed?"Есть HTTPS-ответ":"Есть ошибки",r.stale?"neutral":r.passed?"":"warn");}
function checkReport(p){const r=state.checks?.[p.id];if(!r)return "";const hosts=new Set(r.results.map(v=>new URL(v.url).hostname));const uncovered=p.domains.filter(d=>![...hosts].some(h=>h===d||h.endsWith("."+d)));return `<details class="check-report" data-check-view="${esc(p.id)}" ${checkOpen.get(p.id)?"open":""}><summary>Последняя проверка · ${esc(new Date(r.finished).toLocaleString("ru-RU"))} · ${r.results.filter(v=>v.ok).length}/${r.results.length} проб</summary><p>${r.stale?"Срок актуальности истёк или конфигурация/состояние службы изменились — повторите проверку. ":""}${!r.running?"Проверка при остановленном обходе. ":""}Профиль: ${esc(strategyName(r.strategy))}${r.adaptive?"; автоподбор по трафику включён, метод может отличаться по доменам и протоколам":""}.</p>${r.error?`<p class="fail-text">${esc(r.error)}</p>`:""}<div class="table-scroll"><table class="probe-table"><thead><tr><th>HTTPS-адрес</th><th>TLS / IP</th><th>Ответ / время</th><th>Результат</th></tr></thead><tbody>${r.results.map(v=>`<tr><td>${esc(v.url)}</td><td>${esc(v.tls)} / ${esc(v.family)}</td><td>${v.status||"—"} · ${v.duration_ms} мс</td><td class="${v.ok?"ok-text":"fail-text"}">${v.ok?"Есть ответ":esc(v.error||"Ошибка")}</td></tr>`).join("")}</tbody></table></div><small>Контрольные адреса не покрывают весь сервис.${uncovered.length?" Домены без отдельной HTTPS-пробы: "+esc(uncovered.join(", "))+".":""} Отчёт хранится до перезапуска Pilot. Проверка не запускает подбор; её трафик может наблюдаться включённым адаптивным движком.</small><a class="button-link" href="#adaptive">Наблюдаемые методы доменов →</a></details>`;}
let batch = null, batchMessage = "", singleCheck = null;
function cancelBatch() {
  if (batch) batch.abort();
}
function updateBatchButtons() {
  const start = $("#check-all");
  if (!start) return;
  start.disabled = !!batch || checkPending.size > 0 || manualBlocked() || lostContact || !state.config.profiles.length;
  $("#check-stop").hidden = !batch;
  $("#batch-status").textContent = batchMessage;
}
async function checkAllServices() {
  if (batch || checkPending.size || manualBlocked() || lostContact) return;
  const controller = new AbortController();
  const profiles = state.config.profiles.slice();
  const session = panelSession;
  let completed = 0, skipped = 0;
  batch = controller;
  try {
    for (const profile of profiles) {
      if (controller.signal.aborted) break;
      batchMessage = `Проверяется ${profile.name} · ${completed + skipped + 1} из ${profiles.length}`;
      checkPending.add(profile.id);
      updateCheckReports();
      try {
        await api("check", "POST", {profile: profile.id}, controller.signal);
        completed++;
      } catch (error) {
        if (error.status !== 429) throw error;
        skipped++;
      } finally {
        checkPending.delete(profile.id);
      }
    }
    batchMessage = (controller.signal.aborted ? `Проверка остановлена. Завершено: ${completed} из ${profiles.length}.` : `Проверено сервисов: ${completed} из ${profiles.length}.`) +
      (skipped ? ` Недавно проверены: ${skipped}.` : "");
  } catch (error) {
    batchMessage = controller.signal.aborted
      ? `Проверка остановлена. Завершено: ${completed} из ${profiles.length}.`
      : `Проверка прервана. Завершено: ${completed} из ${profiles.length}. ${error.message}`;
  } finally {
    batch = null;
    if (session === panelSession && signedIn) {
      updateCheckReports();
      try { await refresh(); } catch (error) { notify(error.message, true); }
    } else {
      batchMessage = "";
    }
  }
}
function manualBlocked(){return Boolean(state?.busy && !state?.background_search_running);}
function checkDisabled(){return !signedIn || lostContact || manualBlocked() || !!batch || checkPending.size>0;}
async function checkOneService(id) {
  if (checkDisabled()) return;
  const controller = new AbortController();
  const session = panelSession;
  singleCheck = controller;
  checkPending.add(id);
  updateCheckReports();
  try {
    await api("check", "POST", {profile:id}, controller.signal);
  } catch (error) {
    if (!controller.signal.aborted && session === panelSession && signedIn) notify(error.message, true);
  } finally {
    singleCheck = null;
    checkPending.delete(id);
    if (session === panelSession && signedIn) {
      updateCheckReports();
      try { await refresh(); } catch (error) {
        if (session === panelSession && signedIn) notify(error.message, true);
      }
    }
  }
}
function bindCheckActions(root=document){
  root.querySelector("#check-all")?.addEventListener("click", checkAllServices);
  root.querySelector("#check-stop")?.addEventListener("click", cancelBatch);
  updateBatchButtons();
  root.querySelectorAll("[data-check-view]").forEach(el=>el.addEventListener("toggle",()=>checkOpen.set(el.dataset.checkView,el.open)));
  root.querySelectorAll("[data-check-profile]").forEach(button=>{
    button.disabled=checkDisabled();
    button.addEventListener("click",()=>checkOneService(button.dataset.checkProfile));
  });
}
function updateCheckReports(){updateBatchButtons();document.querySelectorAll("[data-check-profile]").forEach(el=>{el.disabled=checkDisabled();el.textContent=checkPending.has(el.dataset.checkProfile)?"Проверяется…":"Проверить";});for(const p of state.config.profiles){const status=$(`[data-check-status="${p.id}"]`),report=$(`[data-check-report="${p.id}"]`);if(status)status.innerHTML=checkStatus(p);if(report){report.innerHTML=checkReport(p);report.querySelectorAll("[data-check-view]").forEach(el=>el.addEventListener("toggle",()=>checkOpen.set(el.dataset.checkView,el.open)));}}}
const profileDrafts = new Map(), profileOpen = new Map();
let serviceCatalogOpen = false, serviceCatalogQuery = "", selectedTuneProfile = "", serviceMutationPending = false;
function presetNote(id) {
  return (state.service_presets || []).find((s) => s.profile.id === id)?.note || "HTTPS-пробы не подтверждают работу приложения, видео и голоса.";
}
function servicePresetState(p) {
  const existing = state.config.profiles.some((v) => v.id === p.id);
  const conflict = state.config.profiles.some((v) => v.domains.some((d) => p.domains.some((other) => d === other || d.endsWith("." + other) || other.endsWith("." + d))));
  return { existing, conflict, blocked: existing || conflict || state.config.profiles.length >= 12 || manualBlocked() || serviceMutationPending };
}
function updateServiceButtons() {
  document.querySelectorAll("#performance-rescue-apply, #telegram-native-toggle").forEach(button => {
    button.disabled = serviceMutationPending;
  });
  const restore = $("#youtube-restore");
  if (restore) restore.disabled = manualBlocked() || serviceMutationPending;
  const video = $("#youtube-video-check");
  if (video) video.disabled = manualBlocked() || videoProbeBusy;
  document.querySelectorAll("[data-add-service]").forEach((button) => {
    const preset = (state.service_presets || []).find((p) => p.profile.id === button.dataset.addService);
    button.disabled = !preset || servicePresetState(preset.profile).blocked;
  });
  document.querySelectorAll("[data-expand-domains]").forEach(b=>{b.disabled=manualBlocked()||serviceMutationPending;});
  document.querySelectorAll("[data-domain-sync]").forEach(b=>{b.disabled=manualBlocked()||serviceMutationPending;});
  const customButton=$("#custom-service-form button[type=submit]");if(customButton)customButton.disabled=manualBlocked()||serviceMutationPending||state.config.profiles.length>=12;
  document.querySelectorAll("[data-profile-card]").forEach(card=>{const p=state.config.profiles.find(p=>p.id===card.dataset.profileCard);if(!p)return;const summary=card.querySelector(".profile-summary small");if(summary)summary.textContent=strategyName(p.strategy)+" · доменов: "+p.domains.length;if(!profileDrafts.has(p.id)){const box=$(`#domains-${p.id}`);if(box)box.value=p.domains.join("\n");const select=$(`#strategy-${p.id}`);if(select)select.value=p.strategy;}});
  document.querySelectorAll("[data-save-profile]").forEach((b) => { b.disabled = manualBlocked() || serviceMutationPending; });
  document.querySelectorAll("[data-remove-service]").forEach((b) => { b.disabled = manualBlocked() || serviceMutationPending || state.config.profiles.length <= 1; });
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
  return `<details class="panel service-profile" data-profile-card="${esc(p.id)}" ${profileOpen.get(p.id) ? "open" : ""}><summary><span class="profile-summary">${logo(p)}<span><strong>${esc(p.name)}</strong><small>${esc(strategyName(p.strategy))} · доменов: ${p.domains.length}</small></span></span></summary><div class="profile-editor"><p class="subtle">${esc(presetNote(p.id))}</p><label for="strategy-${esc(p.id)}">Текущая стратегия</label><select id="strategy-${esc(p.id)}">${strategyOptions(edit.strategy)}</select><details><summary>Домены и проверки</summary><label for="domains-${esc(p.id)}">Один домен на строку, поддомены включаются автоматически</label><textarea rows="4" id="domains-${esc(p.id)}">${esc(edit.domains.join("\n"))}</textarea><div class="form-actions"><label>Импорт списка hosts / TXT<input type="file" data-hostlist-import="${esc(p.id)}" accept=".txt,.hosts,.list,text/plain"></label><button type="button" data-hostlist-export="${esc(p.id)}">Скачать список доменов</button></div><small>Импорт добавляет домены к черновику, не меняет DNS-адреса. Для применения нажмите «Сохранить».</small>${(state.service_presets||[]).some(s=>s.profile.id===p.id)?`<button type="button" data-expand-domains="${esc(p.id)}" ${manualBlocked()?"disabled":""}>Дополнить домены из каталога</button>`:""}<div class="field"></div><label>Контрольные запросы</label>${edit.targets.map((t, i) => `<div class="target-editor" data-profile="${esc(p.id)}" data-target="${i}"><label>Адрес HTTPS</label><input type="url" data-field="url" value="${esc(t.url)}"><div class="target-small"><div><label>HTTP</label><input type="number" min="200" max="399" data-field="status" value="${t.status}"></div><div><label>Фрагмент ответа</label><input data-field="contains" value="${esc(t.contains || "")}" placeholder="Необязательно"></div></div></div>`).join("")}<small>Каждый адрес проверяется с TLS 1.2 и TLS 1.3. HTTP 403/451 и ошибки сертификата не считаются успешным обходом. Контрольные адреса можно изменить под нужный сценарий.</small></details>${domainSourceEditor(edit)}<div class="profile-buttons"><button data-save-profile="${esc(p.id)}" ${manualBlocked() ? "disabled" : ""}>Сохранить</button><button data-check-profile="${esc(p.id)}">Проверить сервис</button><button data-tune-profile="${esc(p.id)}" class="primary" ${!state.running || manualBlocked() ? "disabled" : ""}>Подобрать ↗</button><button data-remove-service="${esc(p.id)}" ${manualBlocked() || state.config.profiles.length <= 1 ? "disabled" : ""}>Удалить профиль</button></div><div data-check-status="${esc(p.id)}">${checkStatus(p)}</div><div data-check-report="${esc(p.id)}">${checkReport(p)}</div></div></details>`;
}
function parseLocalHostlist(raw){
 if(raw.length>262144)throw Error("Список слишком большой (максимум 256 КиБ).");
 const result=new Set();let skipped=0;
 for(const row of raw.split(/\r?\n/)){
  const line=row.replace(/#.*$/,"").trim();if(!line||line.startsWith(";"))continue;
  const parts=line.split(/\s+/);
  if(parts.length>1&&(/^(?:\d{1,3}\.){3}\d{1,3}$/.test(parts[0])||/^[0-9a-f:]+:[0-9a-f:]*$/i.test(parts[0])))parts.shift();
  for(let domain of parts){
   domain=domain.toLowerCase().replace(/^\*\./,"").replace(/\.$/,"");
   if(domain.length>253||!domain.includes(".")||/^(?:\d{1,3}\.){3}\d{1,3}$/.test(domain)||!domain.split(".").every(label=>/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))||/\.(?:local|localhost|internal)$/.test(domain)){skipped++;continue;}
   result.add(domain);if(result.size>300)throw Error("В списке больше 300 доменов; разделите файл.");
  }
 }
 if(!result.size)throw Error("В файле нет допустимых доменов.");
 return {domains:[...result],skipped};
}
function mergeLocalHostlist(existing,incoming){
 const all=[...new Set([...existing,...incoming])];
 const result=all.filter(d=>!all.some(parent=>parent!==d&&d.endsWith("."+parent)));
 if(result.length>300)throw Error("В профиле может быть максимум 300 доменов.");
 return result;
}
function expandedProfileDomains(p,preset,profiles){
 const incoming=preset.profile.domains.filter(d=>!(p.source_exclusions||[]).some(x=>d===x||d.endsWith("."+x)||x.endsWith("."+d))&&!profiles.some(other=>other.id!==p.id&&other.domains.some(x=>d===x||d.endsWith("."+x)||x.endsWith("."+d))));
 const all=[...new Set([...p.domains,...incoming])];
 return all.filter(d=>!all.some(parent=>parent!==d&&d.endsWith("."+parent)));
}
function domainSourceEditor(p){
 const preset=(state.service_presets||[]).some(s=>s.profile.id===p.id),fromPilot=p.domain_source?.startsWith("pilot:");
 const source=p.domain_source||"",mode=source.startsWith("pilot:")?"pilot":source?"url":"";
 return `<details class="domain-source-editor"><summary>Домены из интернета</summary><label>Источник<select id="source-mode-${esc(p.id)}"><option value="">Вручную</option>${preset||fromPilot?`<option value="pilot" ${mode==="pilot"?"selected":""}>Подписанный каталог Pilot</option>`:""}<option value="url" ${mode==="url"?"selected":""}>HTTPS-ссылка на список</option></select></label><label>Ссылка<input id="source-url-${esc(p.id)}" type="url" value="${esc(mode==="url"?source:"")}" placeholder="https://example.com/domains.txt"></label><label class="check-label"><input type="checkbox" id="source-auto-${esc(p.id)}" ${p.auto_domains?"checked":""}>Обновлять раз в сутки</label><details><summary>Контрольная сумма и исключения</summary><label>SHA-256 файла, необязательно<input id="source-sha-${esc(p.id)}" value="${esc(p.domain_sha256||"")}" maxlength="64"></label><label>Не добавлять из источника<textarea id="source-exclusions-${esc(p.id)}" rows="3">${esc((p.source_exclusions||[]).join("\n"))}</textarea></label></details><p class="subtle">Загружаются новые домены; охват ваших доменов сохраняется. Вложенные домены объединяются с родительским. Для удаления домена вручную добавьте его в исключения источника или отключите автообновление. Применение новых доменов кратко перезапустит работающий движок.</p><small>Последнее обновление: ${Date.parse(p.domains_updated)>0?esc(new Date(p.domains_updated).toLocaleString("ru-RU")):"ещё не выполнялось"}</small><button data-domain-sync="${esc(p.id)}">Сохранить и загрузить домены</button></details>`;
}
function customServiceForm(){return `<details class="panel custom-service"><summary>Добавить свой сервис</summary><form id="custom-service-form"><div class="two-fields"><label>Название<input name="name" maxlength="80" required></label><label>Контрольный HTTPS-адрес<input name="url" type="url" required placeholder="https://example.com/"></label></div><label>Домены, по одному на строку<textarea name="domains" rows="3" placeholder="example.com"></textarea></label><label>Ожидаемый фрагмент ответа<input name="contains" required maxlength="512" placeholder="Текст, подтверждающий нужный ответ"></label><details><summary>Домены из интернета</summary><label>HTTPS-ссылка на список<input name="source" type="url" placeholder="https://example.com/domains.txt"></label><label class="check-label"><input type="checkbox" name="auto" checked>Обновлять раз в сутки, если задан источник</label></details><p class="subtle">Нужен список доменов, по одному на строку. Если домены не введены, начальный домен берётся из контрольного адреса. Новый профиль начинает без вмешательства.</p><button class="primary" type="submit">Добавить сервис</button></form></details>`;}

let videoProbeBusy=false, videoProbeReport=null;
function videoProbeMarkup(){
  const r=videoProbeReport;
  if (!r && state.last_video_check) {
    const last = state.last_video_check;
    return `<p>${badge(last.ok ? "Фрагмент видео получен" : "Видео не подтверждено", last.ok ? "neutral" : "warn")}</p>
      <small>Последний замер · ${esc(new Date(last.finished).toLocaleString("ru-RU"))} · HTTP ${Number(last.status) || "—"} · ${Number(last.bytes)} Б · ${Number(last.duration_ms)} мс</small>`;
  }
  if(!r)return '<small>Видео ещё не проверялось. Успех HTTPS-страницы не подтверждает загрузку видеопотока.</small>';
  return `<p>${badge(r.ok?"Фрагмент видео получен":"Видео не подтверждено",r.ok?"neutral":"warn")}</p>
  <p>${r.error?esc(r.error):r.ok?"Ответ содержит полный видеофрагмент с проверенным диапазоном.":"Фрагмент не получен."}</p>
  <small>HTTP: ${r.status||"—"} · MIME: ${esc(r.media_type||"—")} · диапазон: ${esc(r.content_range||"—")}<br>
  Получено: ${Number(r.bytes||0)} Б · заголовки: ${Number(r.headers_ms||0)} мс · первый блок: ${r.first_byte_ms==null?"—":Number(r.first_byte_ms)+" мс"} · всего: ${Number(r.duration_ms||0)} мс</small>`;
}
function youtubeVideoCheckPanel(){
  if(!state.config.profiles.some(p=>p.id==="youtube"))return "";
  return `<section class="panel video-check-panel"><div class="panel-head"><h2>YouTube · проверить реальные байты видео</h2></div>
  <p class="subtle">Вставьте временную HTTPS-ссылку <code>videoplayback</code> с <code>*.googlevideo.com</code>, полученную при запуске видео. Адрес с подписью используется только в оперативной памяти при проверке и не добавляется в настройки, журнал или диагностику.</p>
  <div class="field"><label for="youtube-video-url">Временная ссылка на видео</label>
  <input id="youtube-video-url" type="password" autocomplete="off" spellcheck="false" maxlength="8192" placeholder="https://rr…googlevideo.com/videoplayback?…" />
  <small>Проверка из роутера: HTTP 206, MIME video/*, Content-Range, первые 64 КиБ и время загрузки. Работа телевизора и плавность воспроизведения проверяются отдельно.</small></div>
  <div class="form-actions"><button type="button" id="youtube-restore" class="primary" ${manualBlocked()?"disabled":""}>Вернуть fake-host-md5 и закрепить YouTube</button></div>
  <p class="subtle">Восстановление дополняет охват YouTube/CDN из каталога (с учётом исключений), включает ранее работавший метод и запрещает автоподбору автоматически менять его по ответу страницы. Настройки других сервисов сохраняются. Изменение кратко перезапустит движок.</p>
  <div class="form-actions"><button type="button" id="youtube-video-check" ${videoProbeBusy||manualBlocked()?"disabled":""}>Проверить видеофрагмент</button></div>
  <div id="youtube-video-report" aria-live="polite">${videoProbeMarkup()}</div></section>`;
}
function bindYoutubeRestore(){
  const button=$("#youtube-restore");
  if(!button)return;
  button.addEventListener("click",()=>serviceAction(async()=>{
    const c=structuredClone(state.config);
    const youtube=c.profiles.find(p=>p.id==="youtube");
    if(!youtube)throw new Error("Профиль YouTube не найден");
    if(!c.youtube_stable || youtube.strategy!=="fake-host-md5"){
      if(!window.confirm("Включить стабильный метод fake-host-md5 для YouTube? Другие сервисы останутся без изменений, движок кратко перезапустится."))return;
    }
    const preset=(state.service_presets||[]).find(s=>s.profile.id==="youtube");
    if(preset)youtube.domains=expandedProfileDomains(youtube,preset,c.profiles);
    youtube.strategy="fake-host-md5";
    c.youtube_stable=true;
    await api("config","PUT",c);
    profileDrafts.delete("youtube");
    notify("YouTube: fake-host-md5 закреплён; домены из каталога дополнены. Проверь видео.");
  }));
}
function bindYoutubeVideoCheck(){
  const button=$("#youtube-video-check"),input=$("#youtube-video-url"),report=$("#youtube-video-report");
  if(!button||!input||!report)return;
  button.addEventListener("click",async()=>{
    if(videoProbeBusy)return;
    const url=input.value.trim();
    input.value="";
    if(!url){notify("Вставьте временный адрес видеопотока",true);return;}
    videoProbeBusy=true;button.disabled=true;button.textContent="Проверяется…";
    try {
      videoProbeReport=await api("youtube/video-test","POST",{url});
    }catch(e){
      videoProbeReport={ok:false,error:e.message};
    }finally {
      videoProbeBusy=false;button.disabled=manualBlocked();button.textContent="Проверить видеофрагмент";
      if(report.isConnected)report.innerHTML=videoProbeMarkup();
    }
  });
}

// A running six-minute background search must not lock users out of a manual
// recovery. Cancel only the cancellable tuning/search operation, wait until
// the controller's normal rollback frees the operation lock, then apply a
// fresh copy of config rather than overwriting another completed change.
async function applyConfigAfterActiveSearch(change){
  if(manualBlocked())await api("tune/cancel","POST",{});
  for(let i=0;i<24;i++){
    const fresh=await api("status");
    if(!fresh.busy||fresh.background_search_running){
      const c=structuredClone(fresh.config);
      change(c);
      await api("config","PUT",c);
      return;
    }
    await new Promise(done=>setTimeout(done,400));
  }
  throw new Error("Контроллер занят больше 10 секунд. Попробуйте снова после завершения поиска.");
}
function performanceRescuePanel(){
  const paused=Boolean(state.config.background_paused);
  return `<section class="panel" id="performance-rescue">
    <div class="panel-head"><h2>Фоновый автопоиск · пауза без перезапуска</h2></div>
    <p>${badge(paused?"Проверки приостановлены":"Автопоиск доступен",paused?"warn":"neutral")}</p>
    <p class="subtle">Очередь: ${state.domain_queue??"—"}. Если роутер перегружен, можно временно остановить фоновые проверки, подбор и загрузку каталогов. Работающий nfqws2, методы всех сервисов, QUIC, Telegram, YouTube и доменные правила НЕ изменяются.</p>
    <div class="form-actions"><button id="performance-rescue-apply" class="primary" ${serviceMutationPending?"disabled":""}>${paused?"Возобновить фоновые проверки":"Пауза фоновых проверок"}</button></div>
    <small>Сохраняется только флаг background_paused. Движок и firewall не перезапускаются. Если поиск сейчас выполняется, его попытки сначала будут отменены.</small>
  </section>`;
}
function bindPerformanceRescue(){
  const button=$("#performance-rescue-apply");
  if(!button)return;
  button.addEventListener("click",()=>serviceAction(async()=>{
    const paused=Boolean(state.config.background_paused);
    if(!window.confirm(paused?"Возобновить фоновые проверки Pilot?":"Приостановить фоновые проверки без изменения обхода и сети?"))return;
    await applyConfigAfterActiveSearch(c=>{c.background_paused=!paused;});
    notify(paused?"Фоновый автопоиск возобновлён.":"Фоновые проверки приостановлены, обход продолжает работать.");
  }));
}
function telegramNativePanel(){
  const legacy=Boolean(state.config.telegram_mtproto);
  return `<section class="panel" id="telegram-native-panel">
    <div class="panel-head"><h2>Telegram · сквозная маршрутизация</h2></div>
    <p>Направление Telegram через существующий VPN на роутере: без прокси в приложении, секретов и Cloudflare Worker. Настрой IP-маршруты и домены в Geo Routing. Маршруты применяются вручную в KeeneticOS.</p>
    ${legacy?'<p class="subtle">Старый экспериментальный режим обработки MTProto включён. После подтверждения рабочих маршрутов его можно выключить отдельно.</p><button id="telegram-native-toggle" type="button">Выключить старую обработку MTProto</button>':'<p class="subtle">Старая обработка MTProto выключена.</p>'}
    <a href="#geo">Открыть Geo Routing ↗</a>
  </section>`;
}
function bindTelegramNative(){
  const button=$("#telegram-native-toggle");
  if(!button)return;
  button.addEventListener("click",()=>serviceAction(async()=>{
    await applyConfigAfterActiveSearch(c=>{c.telegram_mtproto=false;});
    notify("Старая обработка MTProto выключена. Маршруты Keenetic не изменены.");
  }));
}

function profiles() {
  return heading("Сервисы и домены", "Выберите сервис из каталога. Раскройте профиль для стратегии, доменов и проверок.") + youtubeVideoCheckPanel() + performanceRescuePanel() + telegramNativePanel() + serviceCatalog() + customServiceForm() + `<div class="service-profile-list">${state.config.profiles.map(profileCard).join("")}</div>`;
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
      "Сначала сохранённый метод и опыт коробок; перед сохранением — полная повторная проверка.",
    ) +
    serviceCatalog() + `<div class="tune-intro"><div class="step-card"><b>01 / ИСХОДНОЕ СОСТОЯНИЕ</b><strong>Проверка без обхода</strong><p>Прямые соединения помогают понять, требуется ли вмешательство.</p></div><div class="step-card"><b>02 / ПОИСК</b><strong>TLS 1.2 + TLS 1.3</strong><p>Применяем варианты и повторяем контрольные запросы.</p></div><div class="step-card"><b>03 / ПОДТВЕРЖДЕНИЕ</b><strong>Сохранение или откат</strong><p>Перезапускаем движок и снова проверяем выбранный вариант.</p></div></div><div class="panel"><div class="tune-control"><div class="field"><label for="tune-profile">Сервис</label><select id="tune-profile">${state.config.profiles.map((p) => `<option value="${esc(p.id)}" ${p.id === selectedTuneProfile ? "selected" : ""}>${esc(p.name)}</option>`).join("")}</select></div><button id="tune-start" class="primary" ${!state.running || manualBlocked() ? "disabled" : ""}>Начать подбор ↗</button><button id="tune-cancel" ${!tuneRunning() ? "disabled" : ""}>Отменить</button></div><div class="info-note">Во время подбора движок несколько раз перезапускается. Возможны краткие перебои у активных соединений. QUIC, голос Discord и фактическое воспроизведение видео требуют отдельных испытаний.</div><p id="tune-profile-note" class="subtle">${esc(presetNote(state.config.profiles.find((p) => p.id === selectedTuneProfile)?.id || state.config.profiles[0].id))}</p></div><div id="job-panel">${jobPanel()}</div>`
  );
}
function jobPanel(){
 const domain=domainSearchPanel(),j=state.job;if(!j)return domain+ '<div class="panel empty"><h3>Результатов пока нет</h3><p>Выберите сервис и начните подбор.</p></div>';
 const stages={direct:"Проверка без обхода",starting:"Подготовка",confirm:"Повторная проверка",done:"Завершено"};
 return domain+`<div class="panel"><div class="panel-head"><h2>Результаты · ${esc(j.profile)}</h2>${badge(j.finished?(j.selected?"Сохранено":"Откат"):"В процессе",j.finished&&!j.selected?"warn":"neutral")}</div><p class="job-progress">${esc(stages[j.stage]||strategyName(j.stage))}</p>${j.error?`<div class="notice danger">${esc(j.error)}</div>`:""}<div class="trial-list">${j.trials.map((t,i)=>{
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
    `<form id="settings-form"><div class="settings-grid"><div class="panel"><div class="panel-head"><h2>Подключение</h2></div><div class="field"><label for="wan">WAN-интерфейс</label><input id="wan" value="${esc(c.wan)}" placeholder="Например: ppp0"><small>Имя активного интерфейса провайдера на роутере.</small></div>${toggle("ipv6", "Обрабатывать IPv6", "Включайте при наличии работающего IPv6 у провайдера.", c.ipv6)}${toggle("ppe", "Сохранять видимость handshake", "Первые пакеты без аппаратного ускорения: окно 48 при автоподборе по трафику, иначе 30.", c.ppe)}${toggle("quic", "Обрабатывать QUIC", "Статическая стратегия UDP/443. Подбор HTTPS её не проверяет.", c.quic)}${toggle("discord_voice", "Discord Voice · экспериментально", "Обработка UDP 50000–50099. Нужна проверка реального звонка.", c.discord_voice)}${toggle("youtube_stable", "YouTube · сохранить выбранную стратегию", "Выбранный метод профиля YouTube не будет автоматически заменяться по HTTPS-пробе. Для восстановления используйте кнопку в «Сервисах и доменах».", c.youtube_stable)}</div><div class="panel"><div class="panel-head"><h2>Автоматический подбор</h2></div>${toggle("adaptive", "Автоподбор по трафику", "Обнаруживать повторные ошибки HTTPS и менять метод отдельно для каждого сайта. Сохранённый метод используется первым.", c.adaptive)}<div class="info-note">Новые наблюдения и выбранные методы — в разделе «Коробки обхода». Сетевые ошибки не всегда означают блокировку.</div>${toggle("adaptive_fast_start","Быстрый старт автоподбора","Первые три смены метода в каждом 10-минутном окне: пауза 10 секунд вместо обычной. Затем действует заданная ниже пауза. Два сбоя перед сменой и общие лимиты сохраняются.",c.adaptive_fast_start)}${toggle("adaptive_active","Полный автоподбор","После повторных сбоев самостоятельно проверить прямое подключение и весь каталог на изолированных пробах. Применять только подтверждённый HTTPS-результат без перезапуска движка.",c.adaptive_active)}${toggle("subscription_auto_refresh","Обновлять сохранённую VPN-подписку","Не чаще раза в сутки, при разрешённых фоновых проверках. Маршруты и туннели не меняются.",Boolean(c.subscription_auto_refresh))}${toggle("lightweight","Облегчённый режим (MIPS / слабые роутеры)","Проверка очереди фонового подбора каждые 30 секунд вместо 10. Действующие методы не меняются.",Boolean(c.lightweight))}${toggle("background_paused","Пауза фоновых проверок","Приостановить автоматические проверки и подбор, не перезапуская nfqws2, QUIC и текущие стратегии.",Boolean(c.background_paused))}${toggle("background_other_domains","Проверять посторонние сайты автоматически","По умолчанию выключено для снижения нагрузки. Полный автоподбор работает на настроенных сервисах; неизвестные сайты можно поставить на проверку вручную. Включайте только на мощном роутере.",Boolean(c.background_other_domains))}${toggle("adaptive_reuse", "Использовать опыт коробок", "После повторных сбоев сначала пробовать до трёх методов, давших ответ в похожем контексте за последние семь дней.", c.adaptive_reuse)}<div class="two-fields"><div class="field"><label for="adaptive-cooldown">Пауза между сменами, с</label><input id="adaptive-cooldown" type="number" min="10" max="300" value="${c.adaptive_cooldown_seconds}"></div><div class="field"><label for="adaptive-max-switches">Максимум смен за 10 минут</label><input id="adaptive-max-switches" type="number" min="1" max="20" value="${c.adaptive_max_switches}"></div></div><div class="field"><label for="adaptive-global-max">Общий лимит смен движка за 10 минут</label><input id="adaptive-global-max" type="number" min="1" max="300" value="${c.adaptive_global_max_switches??30}"><small>Для всех коробок вместе. Рабочие методы сохраняются; при достижении лимита смена ждёт окончания окна.</small></div>${toggle("probe_load_guard","Защита фоновых проверок","Откладывать плановые проверки, подбор и загрузку каталогов при CPU ≥65%, load ≥ числу ядер или нехватке памяти. Работающий обход продолжается.",c.probe_load_guard!==false)}${toggle("auto_catalog", "Обновлять каталог стратегий", "Подписанный каталог Pilot: проверка раз в сутки. Новые варианты проверяются при подборе.", c.auto_catalog)}<div class="form-actions"><button type="button" id="catalog-update">Загрузить стратегии сейчас</button></div>${toggle("auto", "Следить за доступностью", "После двух неудачных циклов проверок начинается новый подбор.", c.auto)}<div class="field"></div><div class="two-fields"><div class="field"><label for="interval">Интервал, мин</label><input id="interval" type="number" min="5" max="1440" value="${c.interval_minutes}"></div><div class="field"><label for="timeout">Таймаут запроса, с</label><input id="timeout" type="number" min="2" max="20" value="${c.timeout_seconds}"></div></div><div class="field"><label for="history-retention">Хранение истории, суток (0 — без очистки по сроку)</label><input id="history-retention" type="number" min="0" max="365" value="${c.history_retention_days??0}"><small>Удаляются только группы, последний результат которых старше срока. Настройки и текущие наблюдения сохраняются.</small></div><div class="field"><label for="attempts">Попыток на протокол и адрес</label><input id="attempts" type="number" min="1" max="3" value="${c.attempts}"></div><div class="field"><label for="exclusions">Исключения · по одному домену на строку</label><textarea id="exclusions" rows="6" placeholder="example.com">${esc(c.exclusions.join("\n"))}</textarea><small>Исключённые домены не обрабатываются движком.</small></div></div></div><div class="form-actions"><button type="submit" class="primary" ${manualBlocked() ? "disabled" : ""}>Сохранить настройки ↗</button><small>Изменение параметров обработки трафика перезапустит движок. Параметры фоновых проверок применяются сразу.</small></div></form>` + `<section class="panel"><h2>Резервная копия настроек</h2><p>Включает профили и ручные правила, но не пароли панели и не файлы обучения. Может содержать частные домены и URL — храните локально.</p><div class="form-actions"><button id="settings-backup-download" type="button">Скачать JSON</button><input type="file" id="settings-backup-file" accept=".json,application/json"><button id="settings-backup-restore" type="button">Восстановить настройки</button></div></section>`
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
let panelSession = 0, refreshSerial = 0, statusReads = 0, lastContact = 0, lostContact = false;
function showContact(online) {
  lostContact = !online;
  $("#connection-banner").hidden = online;
  $("#connection-indicator").textContent = online ? "Локальное подключение" : "Нет связи";
  $(".local-indicator").classList.toggle("offline", !online);
  if (online) lastContact = Date.now();
  else {
    $("#connection-info").textContent = "Нет ответа от панели." +
      (lastContact ? ` Последние данные получены в ${new Date(lastContact).toLocaleTimeString("ru-RU")}.` : "");
    updateBatchButtons();
    document.querySelectorAll("[data-check-profile]").forEach(el=>{el.disabled=true;});
  }
}
async function refresh(render = false, poll = false) {
  const serial = ++refreshSerial;
  statusReads++;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 10000);
  try {
    const next = await api("status", "GET", undefined, controller.signal);
    if (serial !== refreshSerial) return;
    state = next;
    showContact(true);
  } catch (error) {
    if (serial !== refreshSerial) return;
    if (signedIn) showContact(false);
    throw error;
  } finally {
    clearTimeout(timeout);
    statusReads--;
  }
  updateChrome();
  if (!signedIn) return;
  updateServiceButtons();
  updateCheckReports();
  if (render) renderPage();
  else if (page === "overview") {
    // Keep the targets of any slow telemetry requests attached across status polls.
    const widgets = [$("#network-telemetry"), $("#overview-boxes")].filter(Boolean);
    $("#content").innerHTML = overview();
    for (const widget of widgets) document.getElementById(widget.id).replaceWith(widget);
    bindActions();
    loadTelemetry();loadOverviewBoxes();
  } else if (page === "tune") {
    $("#job-panel").innerHTML = jobPanel();
    bindTrials();
    $("#tune-start").disabled = !state.running || manualBlocked();
    $("#tune-cancel").disabled = !tuneRunning();
    bindDomainRetry();
  } else {
    let details;
    if (page === "device") details = loadDevice();
    if (page === "updates") details = Promise.all([loadUpdates(), loadSourceUpdates()]);
    if (page === "adaptive") details = loadAdaptive();
    if (!poll) await details;
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
function validateIndependentVPN(type, data){
 if(type==="ssh"){
  const host=String(data.host||"").trim(), user=String(data.user||"").trim(), port=String(data.port||"");
  if(!/^(?:[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}|\[[0-9a-fA-F:]+\])$/.test(host)||! /^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$/.test(user)||!/^\d{1,5}$/.test(port)||Number(port)<1||Number(port)>65535)throw Error("Проверь адрес, логин и порт SSH.");
  return "Параметры SSH имеют допустимый формат. Для полноценного туннеля требуется поддержка на сервере.";
 }
 if(!["wg","awg","openvpn"].includes(type))throw Error("Неизвестный тип VPN.");
 const config=String(data.config||"");
 if(!config.trim()||config.length>65536||config.includes("\0"))throw Error("Выбери текстовую конфигурацию до 64 КБ.");
 const lines=config.split(/\r?\n/).map(s=>s.trim()).filter(s=>s&&!/^[#;]/.test(s));
 if(type==="openvpn"){
  if(lines.some(s=>/^(?:--)?(?:up|down|route-up|route-pre-down|ipchange|learn-address|client-connect|client-disconnect|plugin|script-security|management|config|tls-verify|auth-user-pass-verify)(?:\s|$)/i.test(s)))throw Error("Обнаружены внешние команды или потенциально опасные директивы OpenVPN.");
  if(!lines.some(s=>/^(?:--)?remote\s+\S+/i.test(s))||!lines.some(s=>/^(?:--)?(?:client|tls-client)(?:\s|$)/i.test(s)))throw Error("Нужен клиентский профиль OpenVPN с remote.");
 }else{
  let section="",iface=false,peer=false,key=false,remote=false,pub=false,allowed=false;
  for(const line of lines){
   if(/^\[(Interface|Peer)\]$/i.test(line)){section=line.toLowerCase();iface||=section==="[interface]";peer||=section==="[peer]";continue;}
   const m=line.match(/^([a-zA-Z][a-zA-Z0-9]*)\s*=\s*(.+)$/);
   if(!m||!section)throw Error("Неверная структура профиля WireGuard.");
   const field=m[1].toLowerCase(), value=m[2].trim();
   if(["preup","postup","predown","postdown","saveconfig","table"].includes(field))throw Error("Профиль содержит команды или собственные правила маршрутизации.");
   if(section==="[interface]"&&field==="privatekey")key=/^[A-Za-z0-9+/]{43}=$/.test(value);
   if(section==="[peer]"&&field==="publickey")pub=/^[A-Za-z0-9+/]{43}=$/.test(value);
   if(section==="[peer]"&&field==="endpoint")remote=!!value;
   if(section==="[peer]"&&field==="allowedips")allowed=!!value;
   if(type==="wg"&&/^(?:jc|jmin|jmax|s[1-4]|h[1-4]|i[1-5])$/.test(field))throw Error("Это параметры AmneziaWG — выбери соответствующий тип.");
  }
  if(!iface||!peer||!key||!pub||!remote||!allowed)throw Error("В профиле отсутствуют обязательные параметры Interface/Peer.");
 }
 return "Основные параметры найдены. Это проверка формата, не проверка подключения.";
}
function dnsProbePanel(){return `<section class="panel"><h2>Защищённый DNS</h2><p>Проверка DNS с роутера через текущую маршрутизацию, без изменения настроек. Системный резолвер ОС не показывает, какой защищённый протокол настроен в KeeneticOS.</p><label>Имя DNS-сервера (TLS)<input id="dns-server" placeholder="dns.adguard-dns.com" maxlength="253"></label><label>IP для подключения (необязательно)<input id="dns-address" placeholder="94.140.14.14" maxlength="45"></label><label>Протокол<select id="dns-protocol"><option value="all">Системный + DoH / DoT / DoQ</option><option value="system">Системный DNS</option><option>DoH</option><option>DoT</option><option>DoQ</option></select></label><details><summary>Что нужно установить на Keenetic / OpenWrt</summary><p>Keenetic с Entware: проверьте <code>opkg list | grep -E 'knot|kdig|ca-certificates'</code>, установите доступный пакет kdig (обычно knot-dig) и CA-сертификаты. OpenWrt: аналогично проверьте список пакетов opkg либо apk в зависимости от версии. Поддержка QUIC зависит от сборки kdig; проверьте <code>kdig -h</code>. Pilot пакеты автоматически не устанавливает.</p></details><button id="dns-probe" type="button">Проверить DNS</button><pre id="dns-probe-result" aria-live="polite">Не проверено</pre></section>`;}
let subscriptionBusy=false;
function describeSubscription(d){return "Записей: "+d.total+" · "+Object.entries(d.types||{}).map(([k,v])=>k+": "+v).join(", ")+" · неизвестных: "+d.unknown+". Доступность серверов не проверена.";}
function previewLocalSubscription(raw){
 let value=raw.trim();if(!value.includes("://")){try{const decoded=new TextDecoder().decode(Uint8Array.from(atob(value.replace(/\s/g,"").replace(/-/g,"+").replace(/_/g,"/")),c=>c.charCodeAt(0)));if(decoded.includes("://"))value=decoded;}catch{}}
 const types={},known=new Set(["vless","vmess","trojan","ss","ssr","hysteria2","hy2","tuic","wireguard"]);let total=0,unknown=0;
 for(const line of value.split("\n").map(x=>x.trim())){if(!line||line.startsWith("#"))continue;if(++total>2000)throw Error("Слишком много записей.");const match=/^([a-z0-9]+):\/\/(.+)$/i.exec(line);let type=match?.[1].toLowerCase();if(!known.has(type)||!match[2].trim()){unknown++;continue;}if(type==="hy2")type="hysteria2";types[type]=(types[type]||0)+1;}
 if(!Object.keys(types).length)throw Error("Не найдены поддерживаемые URI; YAML и JSON пока не поддерживаются.");return {total,types,unknown};
}
function showSubscriptionDialog(message){
 let dialog=$("#subscription-dialog");
 if(!dialog){dialog=document.createElement("dialog");dialog.id="subscription-dialog";dialog.setAttribute("aria-labelledby","subscription-title");dialog.innerHTML='<h2 id="subscription-title">VPN · предпросмотр</h2><p id="subscription-dialog-result" role="status" aria-live="polite">Результатов пока нет.</p><button type="button" id="subscription-close">Свернуть</button>';document.body.appendChild(dialog);$("#subscription-close").onclick=()=>dialog.close();dialog.addEventListener("click",e=>{const r=dialog.getBoundingClientRect();if(e.target===dialog&&(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom))dialog.close();});}
 if(message!==undefined)$("#subscription-dialog-result").textContent=message;
 if(!dialog.open)dialog.showModal();
}
document.addEventListener("pilot-logout",()=>{$("#subscription-dialog")?.remove();});
function geoPage(){
 return heading("Geo Routing","План выборочной маршрутизации KeeneticOS 5.2 через собственный VPN. Pilot не применяет команды автоматически.")+dnsProbePanel()+`<section class="panel">
 <details class="geo-fold"><summary>Туннель и политика</summary><p id="geo-tunnels" class="subtle">Поиск VPN-интерфейсов…</p>
 <form id="geo-form"><div class="field"><label for="geo-interface">Имя интерфейса KeeneticOS</label><input id="geo-interface" required maxlength="32" placeholder="OpkgTun10" value="OpkgTun10" autocomplete="off"></div>
 <div class="field"><label for="geo-policy">Имя существующей политики доступа (CLI)</label><input id="geo-policy" required maxlength="32" placeholder="Policy0" autocomplete="off"><small>Проверь имя в конфигурации Keenetic. Не название Wi-Fi.</small></div>
 <div class="form-actions"><button id="geo-probe" type="button">Проверить VPN</button></div><p id="geo-probe-result" class="subtle" aria-live="polite"></p><div id="geo-services">Загрузка списка…</div><div class="form-actions"><button type="submit" id="geo-build" disabled>Создать план</button></div></form>
 <p class="subtle">Ни одна служба не направляется в VPN до ручного применения. Список доменов стартовый и не гарантирует охват всех CDN/авторизации. Учитывай правила доступности сервисов. DNS клиентов должен обслуживать Keenetic. Telegram дополнительно использует официальные IP-сети; проверь поддержку IPv6 в туннеле и назначение политики всем нужным клиентам.</p>
 <div id="geo-output" hidden><label for="geo-commands">Предпросмотр команд CLI</label><textarea id="geo-commands" readonly rows="12" spellcheck="false"></textarea><button id="geo-download" type="button">Скачать команды TXT</button>
 <p>Проверь отсутствие существующих групп pilot-geo-*, резервную копию конфигурации, туннель и политику. Применяй в CLI самой KeeneticOS, не в Entware shell. Проверь доступ из клиента политики перед сохранением конфигурации. Для отката удали только созданные маршруты и группы в настройках Keenetic. Автоматического отката нет.</p></div>
 </details></section><section class="panel" id="vpn-standalone">
 <details class="geo-fold"><summary>Smart VPN Auto · независимое подключение (разработка)</summary>
 <p class="subtle">Проверка формата на устройстве пользователя. SSH-проверка проверяет вход на VPS, но не запускает туннель. Пароль не сохраняется. Для ввода пароля открой панель через HTTPS или защищённый SSH-проброс на localhost.</p>
 <p id="vpn-engines" class="subtle">Поиск VPN-движков…</p>
 <form id="vpn-prepare"><div class="field"><label for="vpn-type">Тип подключения</label><select id="vpn-type"><option value="ssh">VPS по SSH</option><option value="wg">WireGuard</option><option value="awg">AmneziaWG</option><option value="openvpn">OpenVPN</option></select></div>
 <div id="vpn-ssh-fields"><div class="field"><label for="vpn-host">IP или домен сервера</label><input id="vpn-host" maxlength="253" autocomplete="off"></div><div class="field"><label for="vpn-port">Порт SSH</label><input id="vpn-port" type="number" min="1" max="65535" value="22"></div><div class="field"><label for="vpn-user">Логин</label><input id="vpn-user" maxlength="64" autocomplete="off"></div><div class="field"><label for="vpn-password">Пароль VPS</label><input id="vpn-password" type="password" maxlength="512" autocomplete="new-password" spellcheck="false" disabled><div class="form-actions"><button id="vpn-password-toggle" type="button" disabled>Показать пароль</button><button id="vpn-ssh-test" type="button" disabled>Проверить вход на VPS</button></div><small id="vpn-password-hint">Для проверки пароля требуется защищённое подключение к панели.</small></div></div>
 <div id="vpn-config-fields" hidden><label for="vpn-config">Локальный файл конфигурации</label><input id="vpn-config" type="file" accept=".conf,.ovpn,.txt"><p class="subtle">Файл проверяется только в браузере, не отправляется на роутер.</p></div>
 <button type="submit">Проверить формат</button><p id="vpn-prepare-result" aria-live="polite"></p></form></details></section><section class="panel"><h2>VPN-подписки · импорт и обновление</h2><p>Предпросмотр не меняет маршруты и не запускает VPN. Поддерживаются TXT и base64 со списком URI. Для передачи приватных данных используйте HTTPS или SSH-проброс панели на localhost.</p><form id="vpn-subscription-form"><label>HTTPS-ссылка <input id="vpn-subscription-url" type="password" autocomplete="off" maxlength="4096" required></label><div class="form-actions"><button type="submit" value="preview">Предпросмотр ссылки</button><button type="submit" value="save">Сохранить ссылку</button></div></form><div class="field"><label for="vpn-subscription-file">Импорт с компьютера</label><input id="vpn-subscription-file" type="file" accept=".txt,.conf,.b64,.base64,text/plain"><div class="form-actions"><button id="vpn-file-preview" type="button">Предпросмотр файла</button><button id="vpn-file-import" type="button">Импортировать файл</button></div></div><div class="form-actions"><button id="vpn-subscription-show" type="button">Открыть последнее окно</button><button id="vpn-subscription-refresh" type="button">Обновить сохранённую</button><button id="vpn-subscription-delete" type="button">Удалить сохранённую</button></div><p id="vpn-subscription-saved" aria-live="polite"></p><p id="vpn-subscription-result" aria-live="polite"></p></section>`;
}
function bindGeo(){
 const form=$("#geo-form");
 if(!form)return;
 const target=$("#geo-services"),status=$("#geo-tunnels"),button=$("#geo-build"),output=$("#geo-output"),commands=$("#geo-commands");
 const session=panelSession;
 const current=()=>signedIn&&panelSession===session&&form===$("#geo-form");
 const dnsButton=$("#dns-probe"),dnsResult=$("#dns-probe-result");
 dnsButton?.addEventListener("click",async()=>{
  dnsButton.disabled=true;dnsResult.textContent="Проверяется…";
  try{const rows=await api("dns/probe","POST",{server:$("#dns-server").value,address:$("#dns-address").value,protocol:$("#dns-protocol").value});if(current())dnsResult.textContent=rows.map(r=>`${r.protocol} · ${r.server} · ${r.status==="ok"?"Есть DNS-ответ":r.status==="unavailable"?"kdig не найден":r.status==="timeout"?"Таймаут":r.status==="unsupported"?"Протокол не поддерживается":r.error||"Нет подтверждения"} · ${r.milliseconds} мс`).join("\n");}
  catch(e){if(current())dnsResult.textContent=e.message;}
  finally{if(current())dnsButton.disabled=false;}
 });
 const subscription=$("#vpn-subscription-form");
 const renderSaved=d=>{if(!current())return;$("#vpn-subscription-saved").textContent=d.saved?(d.local?"Локальный файл: ":"Сохранённая ссылка: ")+new Date(d.updated).toLocaleString("ru-RU")+" · "+describeSubscription(d.preview):"Сохранённой подписки нет.";$("#vpn-subscription-refresh").disabled=!d.saved||d.local;$("#vpn-subscription-delete").disabled=!d.saved;};
 const show=message=>{if(!signedIn||panelSession!==session)return;showSubscriptionDialog(message);if(current())$("#vpn-subscription-result").textContent=message;};
 const run=async(fn)=>{if(subscriptionBusy)return;subscriptionBusy=true;show("Выполняется…");try{await fn();}catch(err){show("Ошибка: "+err.message);}finally{subscriptionBusy=false;}};
 if(subscription){
  $("#vpn-subscription-refresh").disabled=true;$("#vpn-subscription-delete").disabled=true;
  api("vpn/subscription").then(renderSaved).catch(e=>{if(current())$("#vpn-subscription-saved").textContent=e.message;});
  $("#vpn-subscription-show").onclick=()=>showSubscriptionDialog();
  for(const [id,act] of [["vpn-subscription-refresh","refresh"],["vpn-subscription-delete","delete"]]){
   $("#"+id).onclick=()=>{if(act==="delete"&&!window.confirm("Удалить сохранённую подписку?"))return;run(async()=>{renderSaved(await api("vpn/subscription","POST",{action:act}));show(act==="delete"?"Подписка удалена.":"Подписка обновлена.");});};
  }
  subscription.addEventListener("submit",e=>{
   e.preventDefault();const field=$("#vpn-subscription-url"),url=field.value.trim(),save=e.submitter?.value==="save";
   if(!url||subscriptionBusy)return;
   if(save&&!window.confirm("Заменить сохранённую подписку? Маршруты не изменятся."))return;
   field.value="";
   run(async()=>{const d=await api(save?"vpn/subscription":"vpn/subscription/preview","POST",save?{url,action:"save"}:{url});if(save)renderSaved(d);show((save?"Подписка сохранена. ":"")+describeSubscription(save?d.preview:d));});
  });
  const fileInput=$("#vpn-subscription-file");
  const fileAction=save=>{if(subscriptionBusy)return;const file=fileInput.files[0];if(!file||file.size>512*1024){show("Выберите файл до 512 КиБ.");return;}
   if(save&&!window.confirm("Заменить сохранённую подписку локальным файлом? Маршруты не изменятся."))return;
   run(async()=>{if(!save){show(describeSubscription(previewLocalSubscription(await file.text())));return;}
    if(location.protocol!=="https:"&&!["localhost","127.0.0.1","[::1]"].includes(location.hostname))throw Error("Для импорта используйте HTTPS или SSH-проброс на localhost. Предпросмотр файла доступен без отправки.");
    const response=await fetch("/api/vpn/subscription/file",{method:"POST",headers:{Authorization:"Bearer "+token,"Content-Type":"application/octet-stream"},body:file});
    const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||"HTTP "+response.status);renderSaved(data);show("Файл импортирован. "+describeSubscription(data.preview));fileInput.value="";
   });
  };
  $("#vpn-file-preview").onclick=()=>fileAction(false);
  $("#vpn-file-import").onclick=()=>fileAction(true);
 }
 const prep=$("#vpn-prepare"),type=$("#vpn-type"),result=$("#vpn-prepare-result"),file=$("#vpn-config");
 let prepSerial=0;
 const password=$("#vpn-password"), showPassword=$("#vpn-password-toggle"), sshTest=$("#vpn-ssh-test");
 const secureSSH=location.protocol==="https:"||["localhost","127.0.0.1","[::1]"].includes(location.hostname);
 password.disabled=showPassword.disabled=sshTest.disabled=!secureSSH;
 if(secureSSH)$("#vpn-password-hint").textContent="Пароль передаётся только для проверки входа, после проверки очищается. Нужен предварительно проверенный ключ сервера.";
 showPassword.onclick=()=>{const visible=password.type==="password";password.type=visible?"text":"password";showPassword.textContent=visible?"Скрыть пароль":"Показать пароль";};
 sshTest.onclick=async()=>{
  if(!secureSSH||type.value!=="ssh")return;
  const serial=++prepSerial;
  sshTest.disabled=true;result.textContent="Проверяем SSH-вход с роутера…";
  try{
   const data={host:$("#vpn-host").value.trim(),user:$("#vpn-user").value.trim(),port:$("#vpn-port").value,password:password.value};
   validateIndependentVPN("ssh",data);
   if(!data.password)throw Error("Введи пароль VPS.");
   const promise=api("vpn/ssh-check","POST",data);
   data.password="";password.value="";password.type="password";showPassword.textContent="Показать пароль";
   const answer=await promise;
   if(current()&&serial===prepSerial)result.textContent=answer.status==="authenticated"?"SSH-вход подтверждён. Туннель не запущен.": "Вход не подтверждён: "+(answer.message||"ошибка");
  }catch(err){if(current()&&serial===prepSerial)result.textContent=err.message;}
  finally{if(current()){password.value="";password.type="password";showPassword.textContent="Показать пароль";sshTest.disabled=!secureSSH;}}
 };
 const changeVPN=()=>{prepSerial++;result.textContent="";$("#vpn-ssh-fields").hidden=type.value!=="ssh";$("#vpn-config-fields").hidden=type.value==="ssh";file.value="";password.value="";password.type="password";showPassword.textContent="Показать пароль";};
 type.addEventListener("change",changeVPN);
 prep.addEventListener("input",()=>{prepSerial++;result.textContent="";});
 prep.addEventListener("submit",async e=>{
  e.preventDefault();const serial=++prepSerial,kind=type.value;result.textContent="";
  try{
   let config="";
   if(kind!=="ssh"){
    const f=file.files[0];if(!f||f.size>65536)throw Error("Выбери файл до 64 КБ.");
    config=await f.text();
   }
   if(!current()||serial!==prepSerial)return;
   result.textContent=validateIndependentVPN(kind,{host:$("#vpn-host").value,user:$("#vpn-user").value,port:$("#vpn-port").value,config});
  }catch(err){if(current()&&serial===prepSerial)result.textContent=err.message;}
 });
 api("vpn/capabilities").then(d=>{
  if(!current())return;
  const found=Object.entries(d.engines||{}).filter(([,v])=>v).map(([k])=>k);
  $("#vpn-engines").textContent=found.length?"Найдены исполняемые файлы: "+found.join(", ")+". Поддержка туннелей ядром ещё не проверена.":"VPN-движки не найдены.";
 }).catch(()=>{if(current())$("#vpn-engines").textContent="Не удалось получить сведения о VPN-движках.";});

 api("geo").then(d=>{
  if(!current())return;
  status.textContent=d.tunnels.length?"Обнаружены активные интерфейсы Linux: "+d.tunnels.join(", ")+". Это не подтверждение доступности VPN.":"Поддерживаемые активные интерфейсы не обнаружены. Проверь AWG Manager и имя CLI.";
  target.innerHTML=d.services.map(s=>`<div class="toggle-row"><div><label for="geo-${esc(s.id)}">${esc(s.name)}</label><p>${s.domains.map(esc).join(", ")}${s.networks?.length?` · ${s.networks.length} IP-сетей (IPv4/IPv6)`:""}</p></div><input id="geo-${esc(s.id)}" type="checkbox" value="${esc(s.id)}"></div>`).join("");
  button.disabled=false;
 }).catch(e=>{if(current())status.textContent="Не удалось загрузить: "+e.message;});
 const probeButton=$("#geo-probe"),probeResult=$("#geo-probe-result");
 probeButton.addEventListener("click",async ()=>{
  const name=$("#geo-interface").value.trim();
  probeButton.disabled=true;probeResult.textContent="Проверка HTTPS через "+name+"…";
  try{
   const d=await api("geo/probe","POST",{interface:name});
   if(!current())return;
   const messages={reachable:"HTTPS через туннель доступен",unreachable:"Нет ответа HTTPS",down:"Интерфейс не обнаружен или выключен",unknown:"Доступность не подтверждена"};
   probeResult.textContent=d.interface+": "+(messages[d.state]||messages.unknown)+". Маршруты не менялись.";
  }catch(e){if(current())probeResult.textContent="Ошибка проверки: "+e.message;}
  finally{if(current())probeButton.disabled=false;}
 });
 let serial=0;
 form.addEventListener("input",()=>{serial++;output.hidden=true;commands.value="";button.disabled=false;});
 form.addEventListener("submit",async e=>{
  e.preventDefault();
  const selected=[...target.querySelectorAll("input:checked")].map(n=>n.value);
  const attempt=++serial;button.disabled=true;output.hidden=true;commands.value="";
  try{
   const d=await api("geo/plan","POST",{interface:$("#geo-interface").value.trim(),policy:$("#geo-policy").value.trim(),services:selected});
   if(!current()||attempt!==serial)return;
   commands.value=d.commands;output.hidden=false;notify("План готов. Маршрутизация не изменена.");
  }catch(err){if(current()&&attempt===serial)notify(err.message,true);}
  finally{if(current()&&attempt===serial)button.disabled=false;}
 });
 $("#geo-download").addEventListener("click",()=>{
  if(!commands.value)return;
  const url=URL.createObjectURL(new Blob([commands.value],{type:"text/plain;charset=utf-8"}));
  const a=document.createElement("a");a.href=url;a.download="keenetic-pilot-geo-plan.txt";a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
 });
}
function renderPage() {
  if (batch && location.hash && location.hash !== "#overview") cancelBatch();
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
            : page === "geo" ? geoPage()
            : page === "strategies"
              ? strategyCatalog()
              : page === "adaptive"
                ? heading(
                    "Коробки обхода",
                    "Группы наблюдений, найденные методы и история их проверки.",
                  ) +
                  `<div class="adaptive-heading"><details><summary>Как работают коробки</summary><p>Сходные признаки помогают выбирать кандидатов. Каждое новое соединение проверяется отдельно; группа не означает опознанное устройство провайдера.</p><p>«Скачать диагностику» сохраняет полный снимок методов и истории в JSON, независимо от фильтров. Файл содержит домены и имя WAN-интерфейса; пароли, токены, журналы и контрольные URL не включаются. На роутере ничего не меняется.</p></details><div class="adaptive-actions"><button id="adaptive-download" title="JSON с доменами и историей; без паролей и токенов">Скачать диагностику</button><button id="adaptive-health-check" type="button">Самодиагностика</button><button id="adaptive-refresh">Обновить</button><button id="adaptive-reset-all" class="danger-button" type="button">Очистить все коробки</button></div></div><section id="adaptive-health-result" class="panel" aria-live="polite" hidden></section>${adaptiveToolbar()}<div id="adaptive-results">Загрузка…</div>`
                : page === "connect"
                  ? connectPage()
                  : heading(
                      "Журнал",
                      "События контроллера и движка. Последние 200 строк.",
                      '<button id="refresh-logs">Обновить ↻</button>',
                    ) +
                    '<div class="panel"><h2>Уведомления текущего сеанса</h2><div id="notification-center"></div></div><div class="panel"><pre id="log-view" class="log-view">Загрузка…</pre></div>';
  bindActions();
  if(page==="geo")bindGeo();
  if(page==="overview"){loadTelemetry();loadOverviewBoxes();}
  if(page==="device")loadDevice();
  if(page==="updates"){loadUpdates();loadSourceUpdates();}
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
function bindDomainRetry(){const button=$("#domain-retry");if(!button||button.dataset.bound)return;button.dataset.bound="true";button.addEventListener("click",()=>action(async()=>{await api("domain-search","POST",state.domain_job.scope);notify("Домен поставлен в очередь полного автоподбора");}));}
function bindActions() {
  bindDomainRetry();bindMethodViews();bindTrials();bindCheckActions();bindYoutubeVideoCheck();bindYoutubeRestore();bindTelegramNative();bindPerformanceRescue();
  $("#method-search")?.addEventListener("input",e=>{methodQuery=e.target.value;$("#method-results").innerHTML=methodResults();bindMethodViews();});
  $("#method-family")?.addEventListener("change",e=>{methodFamily=e.target.value;$("#method-results").innerHTML=methodResults();bindMethodViews();});
  $("#update-check")?.addEventListener("click",async e=>{const button=e.currentTarget;button.disabled=true;try{await api("updates/check","POST",{});await loadUpdates(true);}catch(error){notify(error.message,true);button.disabled=false;}});
  $("#update-install")?.addEventListener("click",e=>{if(updatesData?.available&&!updatesData.running)startMonitoredUpdate("updates/install",{version:updatesData.latest},"Подписанный канал",e.currentTarget);});
  $("#update-monitor-open")?.addEventListener("click",()=>openUpdateMonitor());
  $("#custom-service-form")?.addEventListener("submit",event=>{event.preventDefault();serviceAction(async()=>{
    const f=event.target,bytes=new Uint32Array(2);crypto.getRandomValues(bytes);const id="custom-"+[...bytes].map(v=>v.toString(16).padStart(8,"0")).join("");
    const url=f.elements.url.value.trim(),source=f.elements.source.value.trim(),domains=lines(f.elements.domains.value);
    if(!domains.length)domains.push(new URL(url).hostname.toLowerCase());
    const p={id,name:f.elements.name.value.trim(),strategy:"none",domains,targets:[{url,status:200,contains:f.elements.contains.value}],domain_source:source,auto_domains:Boolean(source&&f.elements.auto.checked)};
    await api("services/custom","POST",p);
    let message="Сервис добавлен";if(source){try{await api("services/"+id+"/domains","POST",{});}catch(e){message+="; домены пока не загружены: "+e.message;}}
    notify(message);f.reset();
  });});
  document.querySelectorAll("[data-hostlist-import]").forEach(input=>input.addEventListener("change",async()=>{
    const id=input.dataset.hostlistImport,file=input.files[0];if(!file)return;
    const session=panelSession;
    try{
      if(file.size>262144)throw Error("Файл больше 256 КиБ.");
      const parsed=parseLocalHostlist(await file.text());
      if(!signedIn||session!==panelSession||!input.isConnected)return;
      const draft=readProfileDraft(id);
      draft.domains=mergeLocalHostlist(draft.domains,parsed.domains);
      $("#domains-"+id).value=draft.domains.join("\n");profileDrafts.set(id,draft);
      notify("В черновике "+draft.domains.length+" доменов; пропущено "+parsed.skipped+". Нажмите «Сохранить».");
    }catch(e){if(session===panelSession)notify(e.message,true);}finally{input.value="";}
  }));
  document.querySelectorAll("[data-hostlist-export]").forEach(button=>button.addEventListener("click",()=>{
    const id=button.dataset.hostlistExport,draft=readProfileDraft(id);
    const url=URL.createObjectURL(new Blob([draft.domains.join("\n")+"\n"],{type:"text/plain"}));
    const link=document.createElement("a");link.href=url;link.download=id+"-hostlist.txt";link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
  }));
  document.querySelectorAll("[data-expand-domains]").forEach(button=>button.addEventListener("click",()=>serviceAction(async()=>{
    const id=button.dataset.expandDomains,c=structuredClone(state.config),draft=readProfileDraft(id),preset=state.service_presets.find(s=>s.profile.id===id);
    if(!preset)throw new Error("Сервис отсутствует в каталоге");
    draft.domains=expandedProfileDomains(draft,preset,c.profiles);c.profiles[c.profiles.findIndex(p=>p.id===id)]=draft;
    await api("config","PUT",c);profileDrafts.delete(id);notify("Домены дополнены: "+draft.domains.length+". Стратегия и контрольные запросы сохранены.");
  })));
  document.querySelectorAll("[data-domain-sync]").forEach(button=>button.addEventListener("click",()=>serviceAction(async()=>{
    const id=button.dataset.domainSync,c=structuredClone(state.config),draft=readProfileDraft(id);if(!draft.domain_source)throw new Error("Выберите источник доменов");c.profiles[c.profiles.findIndex(p=>p.id===id)]=draft;
    await api("config","PUT",c);profileDrafts.delete(id);await api("services/"+id+"/domains","POST",{});notify("Домены загружены и проверены");
  })));

  $("#adaptive-refresh")?.addEventListener("click", loadAdaptive);
  $("#adaptive-download")?.addEventListener("click", downloadDiagnostics);
  $("#adaptive-health-check")?.addEventListener("click", showDiagnosticHealth);
  $("#adaptive-reset-all")?.addEventListener("click", (event) => {
    if (!window.confirm("Удалить накопленное обучение и историю всех коробок? Ручные правила сохранятся. Возможен краткий перерыв обхода. Новые подключения начнут обучение заново.")) return;
    const button = event.currentTarget;
    button.disabled = true;
    action(async () => {
      try {
        await api("adaptive/domain", "POST", {host:"", action:"reset_all"});
        adaptiveDetails.clear(); adaptiveDrafts.clear();
        await loadAdaptive();
        notify("Обучение всех коробок очищено; ручные правила сохранены");
      } finally { button.disabled = false; }
    });
  });
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
  bindSettingsBackup();
  renderNotifications();
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
        "youtube_stable",
        "auto",
        "auto_catalog",
        "adaptive",
        "adaptive_reuse",
        "adaptive_active",
        "background_paused",
        "subscription_auto_refresh",
        "lightweight",
        "background_other_domains",
        "adaptive_fast_start",
        "probe_load_guard",
      ].forEach((key) => (c[key] = $(`#${key}`).checked));
      c.adaptive_global_max_switches = Number($("#adaptive-global-max").value);
      c.adaptive_cooldown_seconds = Number($("#adaptive-cooldown").value);
      c.adaptive_max_switches = Number($("#adaptive-max-switches").value);
      c.interval_minutes = Number($("#interval").value);
      c.timeout_seconds = Number($("#timeout").value);
      c.attempts = Number($("#attempts").value);
      c.history_retention_days=Number($("#history-retention").value);
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
  if (!enabled || r.applied_strategy || r.paused || r.pinned || !(r.hold_until > now)) return null;
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
function retryObservationText(r,enabled=true,now=Date.now()/1000){
  if(!enabled||!state?.config?.adaptive_active||!r.retry_at||r.paused||r.pinned||!observationHasContext(r))return "";
  return r.retry_at>now?"Повтор домена не раньше "+new Date(r.retry_at*1000).toLocaleString("ru-RU"):"Домен в очереди повторной проверки";
}
function currentObservationStatus(r, enabled = true) {
  if (r.paused) return "Приостановлен";
  if (r.pinned && r.pinned !== r.strategy) return "Ожидает наблюдений";
  if (r.https_measured) return "HTTPS подтверждён";
  if (retryObservationText(r,enabled)) return "Повторная проверка";
  if (r.applied_strategy && r.applied_strategy !== r.strategy) return "Ожидает новых подключений";
  if (currentHold(r, enabled)) return "Смена отложена";
  if (observationStale(r)) return "Нет свежих наблюдений";
  if (!observationHasContext(r)) return "Сохранённое наблюдение";
  return observedStatus(r.status);
}
function currentObservationReason(r, enabled = true) {
  if (r.paused) return "Действует ручное исключение";
  if (r.pinned && r.pinned !== r.strategy) return "Для закреплённого метода ещё нет наблюдений";
  if (r.https_measured) return "Собственные HTTPS-пробы роутера подтвердили метод. Видео и голос отдельно не проверены.";
  if (r.applied_strategy && r.applied_strategy !== r.strategy) return "Для новых подключений применяется другой метод; строка содержит прежнее наблюдение";
  const hold = currentHold(r, enabled);
  if (hold) return `${hold.label} · до ${new Date(hold.until * 1000).toLocaleString("ru-RU")}`;
  if (observationStale(r)) return "За последние сутки нет записанных наблюдений; метод сохранён.";
  if (!observationHasContext(r)) return "Наблюдение относится к прежнему или неполному контексту подключения";
  return eventReason(r);
}
function observationTone(r, history, enabled) {
  if (!history) {
    if (r.paused || currentHold(r, enabled) || retryObservationText(r,enabled) || (r.pinned && r.pinned !== r.strategy)) return "warn";
    if (observationStale(r) || !observationHasContext(r)) return "neutral";
  }
  return r.status === "failed" ? "fail" : r.status === "connected" ? "" : "warn";
}
function observationTable(rows, history = false, enabled = true) {
  if (!rows.length) return '<p class="subtle">Наблюдений пока нет.</p>';
  return `<table class="probe-table"><thead><tr><th>Сайт / метод</th><th>Результат</th><th>Причина / источник</th><th>Время</th></tr></thead><tbody>${rows.map((r) => `<tr><td>${esc(r.host)}<br><small>${esc(strategyName(history ? r.strategy : r.pinned || r.applied_strategy || r.strategy))}</small></td><td>${badge(history ? observedStatus(r.status) : currentObservationStatus(r, enabled), observationTone(r, history, enabled))}${history && r.event_count > 1 ? `<br><small>Событий: ${r.event_count}</small>` : ""}${!history && r.https_measured ? '<br><small>HTTPS подтверждён роутером</small>' : ""}${!history && r.pinned ? '<br><small>Закреплён вручную</small>' : ""}${retryObservationText(r,enabled)?`<br><small>${esc(retryObservationText(r,enabled))}</small>`:""}</td><td>${esc(history ? eventReason(r) : currentObservationReason(r, enabled))}<br><small>${esc(originLabels[r.origin] || originLabels.restored)}</small></td><td>${history && r.event_count > 1 && r.first_updated !== r.updated ? `${esc(new Date(r.first_updated * 1000).toLocaleString("ru-RU"))}<br>— ` : ""}${esc(new Date(r.updated * 1000).toLocaleString("ru-RU"))}</td></tr>`).join("")}</tbody></table>`;
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
      return `<form class="domain-control" ${controlData(host, pin || scope)}><strong>${esc(host)}</strong><label>Метод для закрепления<select name="strategy">${strategyOptions(current)}</select></label><div class="domain-buttons"><button type="submit" ${manualBlocked() ? "disabled" : ""}>${pin ? "Изменить закрепление" : "Закрепить метод"}</button>${pin ? `<button type="button" data-domain-action="unpin" ${controlData(host, pin)}>Снять закрепление</button>` : ""}${ownPause ? '<button type="button" data-domain-action="resume">Возобновить обработку</button>' : !excluded ? '<button type="button" data-domain-action="pause">Приостановить</button>' : ""}<button type="button" data-domain-action="reset">Сбросить обучение</button></div>${excluded ? `<small>Обработка приостановлена правилом ${esc(excluded)}. ${!ownPause ? "Правила исключения — в настройках." : ""}</small>` : ""}</form>`;
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
    const method = r.pinned || r.applied_strategy || r.strategy;
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
    "Выбран для проверки": 2, "Ожидает новых подключений": 2, "HTTPS подтверждён": 3, "Есть ответ": 3, "Приостановлен": 4,
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
  if (active.every((r) => (r.pinned || r.applied_strategy || r.strategy) === "none" && r.status === "connected"))
    return "Наблюдался ответ без вмешательства. Такой метод не пополняет опыт обхода.";
  return "Методов обхода с наблюдаемым ответом пока нет. Текущие методы показаны выше; для проверки нужны новые подключения к сайту.";
}
function boxCard(b, enabled = true) {
  const domains = [
    ...new Set([...b.hosts, ...b.history].map((r) => r.host)),
  ].sort();
  const history = groupedHistory(b.history.filter(r=>r.updated>=historyCutoff && (!historyHours || r.updated>=Date.now()/1000-historyHours*3600)));
  const eventTotal = history.reduce((sum,r)=>sum+(r.event_count||1),0);
  const allEvents = b.history.reduce((sum,r)=>sum+(r.event_count||1),0);
  return `<article class="panel bypass-box" data-box="${esc(b.id)}">
    <div class="panel-head"><h2>${esc(tlsLabels[b.tls] || tlsLabels.unknown)} · ${esc(b.family === "ipv6" ? "IPv6" : b.family === "ipv4" ? "IPv4" : "IP не определён")}</h2><span class="subtle">Домены: ${domains.length}</span></div>
    <p class="box-health">${esc(boxHealthLabel(b,enabled))} · смены в записанных 10-минутных окнах: ${boxHealth(b,enabled).switches}</p>
    <p class="box-context">${esc(symptomLabels[b.symptom] || symptomLabels.unknown)} · ${esc(b.network === "-" ? "WAN не записан" : b.network)}${b.port ? ":" + b.port : ""}</p>
    <p class="box-domains">${domains.slice(0, 2).map((d) => `<span title="${esc(d)}">${esc(d)}</span>`).join("")}${domains.length > 2 ? `<small>ещё ${domains.length - 2}</small>` : ""}</p>
    <div class="box-methods box-current" aria-label="Текущие методы доменов">${currentBoxMethods(b.hosts, enabled, true)}</div>
    <details data-view-key="${esc(b.id)}/observations"><summary>Домены и методы · ${domains.length}</summary>${currentBoxMethods(b.hosts, enabled)}${observationTable(b.hosts, false, enabled)}</details>
    <details data-view-key="${esc(b.id)}/experience"><summary>Опыт обхода · ${b.methods.length}</summary><div class="box-methods">${b.methods.length ? b.methods.map((m) => `<div class="box-method"><div class="box-method-line"><strong>${esc(m.name)}</strong>${badge(m.reusable ? "Кандидат по опыту" : "Сохранённое наблюдение", m.reusable ? "" : "neutral")}</div><small>${esc(familyLabels[m.family] || familyLabels.other)} · ответ на ${m.hosts} доменах</small></div>`).join("") : `<p class="subtle">${esc(boxExperienceEmpty(b))}</p>`}</div></details>
    ${domainControls(b, domains)}
    <details data-view-key="${esc(b.id)}/history"><summary>История · ${history.length} строк · событий: ${eventTotal} из ${allEvents}</summary><p class="subtle">Это накопленные попытки, не число недоступных сайтов. Фильтр выбирает группы по последнему событию; счётчик группы может включать старые попытки.</p>${observationTable(history, true)}</details>
    <small class="box-updated">Наблюдение: ${esc(new Date(b.updated * 1000).toLocaleString("ru-RU"))}</small>
  </article>`;
}
async function showDiagnosticHealth(event) {
  const button = event.currentTarget, target = $("#adaptive-health-result");
  if (!target) return;
  button.disabled = true;
  target.hidden = false;
  target.textContent = "Читаем состояние роутера…";
  try {
    const report = await api("diagnostics");
    if (target !== $("#adaptive-health-result") || !signedIn) return;
    const summary = report.history_summary;
    if (!summary || !Array.isArray(report.health_findings)) {
      target.textContent = "Этот сервер ещё не поддерживает расширенную диагностику.";
      return;
    }
    const labels = {
      enabled_service_not_running: "Служба включена, но не запущена",
      service_reported_error: "Служба сообщила об ошибке",
      observation_read_error: "Не удалось прочитать наблюдения",
      observation_slot_needs_review: "Один из файлов наблюдений требует проверки",
      no_recent_saved_observations: "Нет сохранённых наблюдений за последние сутки; это не доказывает сбой",
      active_search_client_hello_missing: "Последний поиск не получил ClientHello",
      low_opt_free_space: "На /opt менее 32 МиБ свободного места",
      other_packet_engines_detected: "Обнаружены другие обработчики пакетов; возможный конфликт требует проверки"
    };
    const windows = {3600:"Час",86400:"Сутки",604800:"Неделя"};
    target.innerHTML = `<h2>Самодиагностика</h2><p class="subtle">Снимок: ${esc(new Date(report.generated_at).toLocaleString("ru-RU"))}. Только чтение, без сетевых проб и изменения настроек.</p><ul>${report.health_findings.length ? report.health_findings.map(code=>`<li>${esc(labels[code]||code)}</li>`).join("") : "<li>В этом снимке известных предупреждений нет.</li>"}</ul><h3>История попыток</h3><p>Записей: ${esc(summary.rows)} · событий: ${esc(summary.events)} · неудачных попыток: ${esc(summary.failed_attempts)}</p><table class="probe-table"><thead><tr><th>Период</th><th>Событий (диапазон)</th><th>Смешанных групп</th></tr></thead><tbody>${(summary.windows||[]).map(w=>`<tr><td>${esc(windows[w.seconds]||w.seconds)}</td><td>${esc(w.confirmed_events===w.possible_events?String(w.confirmed_events):w.confirmed_events+"–"+w.possible_events)}</td><td>${esc(w.mixed_rows)}</td></tr>`).join("")}</tbody></table><small>История ограничена сохранёнными группами. Неудачная попытка не означает недоступность сайта. Диапазон отражает отсутствие точных времён отдельных событий.</small>`;
  } catch (e) {
    if (target === $("#adaptive-health-result")) target.textContent = "Самодиагностика недоступна: " + e.message;
  } finally {
    button.disabled = false;
  }
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
let historyHours=0,historyCutoff=0;
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
    if (filter === "connected") return ["Есть ответ","HTTPS подтверждён"].includes(currentObservationStatus(r, enabled));
    return filter === "probing" && ["Пробуется", "Выбран для проверки", "Ожидает наблюдений", "Ожидает новых подключений"].includes(currentObservationStatus(r, enabled));
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
function renderAdaptive(force = false) {
  const target = $("#adaptive-results");
  if (!target || !adaptiveData) return;
  // Keep native selects and buttons in place while the user interacts with them.
  if (target.querySelector(".domain-control:focus-within") || (!force && target.querySelector("#history-hours:focus"))) {
    $("#adaptive-update").textContent = "Новые данные будут показаны после выхода из выбранного элемента.";
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
    `<div class="panel"><label>История попыток<select id="history-hours">${[[0,"Всё время"],[1,"Последний час"],[24,"Последние сутки"],[168,"Последняя неделя"]].map(([v,t])=>`<option value="${v}" ${historyHours===v?"selected":""}>${t}</option>`).join("")}</select></label><div class="form-actions"><button id="history-hide" type="button">Скрыть старую историю</button><button id="history-show" type="button">Вернуть всю историю</button><button id="history-clear" type="button" class="danger-button">Очистить старые попытки</button><button id="history-restore" type="button">Восстановить опыт до очистки</button></div><small>Только отображение в текущей сессии; опыт, настройки и файл диагностики сохраняются.</small></div>` +
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
  target.querySelector("#history-hours")?.addEventListener("change",e=>{historyHours=Number(e.target.value);renderAdaptive(true);});
  target.querySelector("#history-hide")?.addEventListener("click",()=>{historyCutoff=Math.floor(Date.now()/1000)+1;renderAdaptive();});
  target.querySelector("#history-clear")?.addEventListener("click",()=>{if(!window.confirm("Удалить попытки старше 10 минут? Свежие результаты, текущие наблюдения и правила сохранятся. Возможен краткий перезапуск движка; резервная копия останется на роутере."))return;action(async()=>{await api("adaptive/domain","POST",{host:"",action:"clear_history"});notify("Старая история очищена");await loadAdaptive();});});
  target.querySelector("#history-restore")?.addEventListener("click",()=>{if(!window.confirm("Восстановить наблюдения и историю из последней копии перед очисткой? Более новые наблюдения будут заменены, текущие настройки не изменятся. Возможен краткий перезапуск движка."))return;action(async()=>{await api("adaptive/domain","POST",{host:"",action:"restore_history"});notify("Опыт восстановлен");await loadAdaptive();});});
  target.querySelector("#history-show")?.addEventListener("click",()=>{historyCutoff=0;historyHours=0;renderAdaptive();});
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
async function loadTelemetry(){const target=$("#network-telemetry");if(!target||telemetryLoading||document.hidden)return;telemetryLoading=true;try{const d=await api("device");if(!signedIn||target!==$("#network-telemetry"))return;deviceData=d;const now=Date.now();cpuSamples.push({at:now,value:d.cpu_percent});while(cpuSamples.length>16||cpuSamples[0]?.at<now-60000)cpuSamples.shift();target.innerHTML=telemetryMarkup(d);const chart=$("#cpu-chart");if(chart)chart.innerHTML=cpuChart();}catch(e){if(target===$("#network-telemetry"))target.textContent="Показатели недоступны: "+e.message;}finally{telemetryLoading=false;}}
function boxHealth(b,enabled=true){const stats={connected:0,probing:0,held:0,paused:0,pinned:0,stale:0,switches:0};const now=Date.now()/1000;for(const r of b.hosts){if(r.window_start>0&&r.window_start<=now&&r.window_start+600>now)stats.switches+=r.switches||0;if(r.paused)stats.paused++;else if(observationStale(r))stats.stale++;else if(currentHold(r,enabled))stats.held++;else if(r.status==="connected")stats.connected++;else if(r.pinned)stats.pinned++;else stats.probing++;}return stats;}
function boxHealthLabel(b,enabled){if(!enabled)return `Автоподбор выключен · сохранено наблюдений: ${b.hosts.length}`;const v=boxHealth(b,enabled);return `Ответ: ${v.connected} · проверяются: ${v.probing} · ждут: ${v.held} · пауза: ${v.paused} · закреплены: ${v.pinned} · старые: ${v.stale}`;}
function problemFirst(a,b){const av=boxHealth(a),bv=boxHealth(b);return (bv.probing+bv.held)-(av.probing+av.held)||b.updated-a.updated;}
let boxesLastTarget=null,boxesLastFetch=0;
async function loadOverviewBoxes(){const target=$("#overview-boxes");if(!target||overviewBoxesLoading||document.hidden||(target===boxesLastTarget&&Date.now()-boxesLastFetch<12000))return;overviewBoxesLoading=true;boxesLastTarget=target;boxesLastFetch=Date.now();try{const d=await api("adaptive");if(!signedIn||target!==$("#overview-boxes"))return;const boxes=d.boxes||[],recent=boxes.filter(boxHasRecentTraffic).map(b=>({b,h:boxHealth(b)})).sort((a,b)=>(b.h.probing+b.h.held)-(a.h.probing+a.h.held)||b.b.updated-a.b.updated).map(v=>v.b),saved=boxes.length-recent.length;target.innerHTML=(recent.length?`<div class="table-scroll"><table class="box-overview-table"><thead><tr><th>Группа</th><th>Домены</th><th>Состояния</th></tr></thead><tbody>${recent.map(b=>`<tr><td><a href="#adaptive">${esc(tlsLabels[b.tls]||tlsLabels.unknown)} · ${esc(b.family)}</a></td><td>${new Set(b.hosts.map(r=>r.host)).size}</td><td>${esc(boxHealthLabel(b,d.enabled))}</td></tr>`).join("")}</tbody></table></div>`:'<p>Свежих наблюдений пока нет. При новых подключениях автоподбор продолжится.</p>')+`<small>Сохранённая история: ${saved} коробок. ${!d.enabled?"Автоподбор по трафику выключен.":""}</small>`;}catch(e){if(target===$("#overview-boxes"))target.textContent="Наблюдения недоступны: "+e.message;}finally{overviewBoxesLoading=false;}}
let deviceData=null,deviceLoading=false,updatesData=null,updatesLoading=false,updatesSerial=0;
let sourceUpdatesData=null,sourceUpdatesLoading=false,sourceUpdatesSerial=0;
const lastSourceChecks=new Map(),sourceCheckErrors=new Map();
const releaseNotes={
 "0.2.0-beta.3":["Контрольный выпуск для KN-1811","Коробки, Geo Routing, исправления панели"],
 "0.2.0-beta.4":["DNS-тестер: системный резолвер, DoH, DoT и DoQ","Фильтры истории без удаления обучения","Темы Matrix, тёмная, светлая, Aero, Luna и свои цвета","Сведения о последних проверках обновлений; движок не менялся"],
 "0.2.0-beta.5":["Самодиагностика, предупреждения и диапазоны количества событий","Закрытие всплывающих окон вне области и по Esc; описание версии сохраняет раскрытие","Ручная очистка старой истории и восстановление наблюдений из копии; срок хранения 1–365 суток, по умолчанию выключено","Резервное копирование и восстановление настроек; облегчённый режим фоновых проверок","Импорт одной VPN-подписки, ручное и опциональное ежедневное обновление; без запуска туннелей","Уведомления текущего сеанса","Защитный откат при отказе службы или повторной потере двух контрольных HTTPS-подключений во время обновления; не проверяет скорость"],
 "0.2.0-beta.6":["Импорт и экспорт HostList для каждого сервиса; применение только после сохранения","Импорт локального файла VPN-подписки и предварительный просмотр TXT/base64; без запуска VPN","Приоритет ручных операций над фоновым поиском","Geo Routing: план маршрутов Telegram через существующий туннель; независимый VPS-туннель пока не реализован"]
};
const releaseNotesOpen=new Map();
function rememberReleaseNotes(root){
 root.querySelectorAll("details[data-release-notes]").forEach(el=>releaseNotesOpen.set(el.dataset.releaseNotes,el.open));
}
document.addEventListener("toggle",e=>{
 const el=e.target;
 if(el instanceof Element&&el.isConnected&&el.matches("details[data-release-notes]"))releaseNotesOpen.set(el.dataset.releaseNotes,el.open);
},true);
function releaseNotesHTML(version,scope="installed",defaultOpen=false){
 const key=scope+":"+String(version||"unknown");
 const notes=scope==="Codex"?null:releaseNotes[version];
 const open=releaseNotesOpen.has(key)?releaseNotesOpen.get(key):defaultOpen;
 return `<details class="release-notes" data-release-notes="${esc(key)}" ${open?"open":""}><summary>Что нового${version?" · "+esc(version):""}</summary>${Array.isArray(notes)?`<ul>${notes.map(note=>`<li>${esc(note)}</li>`).join("")}</ul><small>Локальное описание выпуска ChatGPT, не подтверждение источника установленного пакета.</small>`:"<p>Описание этого выпуска пока отсутствует в панели.</p>"}</details>`;
}
const prettyBytes=n=>n==null?"недоступно":n>=1048576?(n/1048576).toFixed(1)+" МиБ":n>=1024?(n/1024).toFixed(1)+" КиБ":Math.round(n)+" Б";
function devicePage(){return heading("Роутер","Данные устройства, на котором запущен Pilot. Недоступные датчики не заменяются примерными значениями.")+`<p class="panel-address">Адрес этой панели · <a href="#overview">${esc(location.origin)}</a></p><div id="device-data">Загрузка…</div><section class="panel"><h2>Проверка среды Keenetic / Entware</h2><p class="subtle">Пассивная проверка по идеям MY.KN. Никакие службы, правила NFQUEUE и подключённые носители не изменяются.</p><div id="preflight-data">Загрузка…</div></section>`;}
async function loadDevice(){const target=$("#device-data");if(!target||deviceLoading)return;deviceLoading=true;try{const d=await api("device");if(target!==$("#device-data"))return;deviceData=d;target.innerHTML=`<div class="stats-grid"><div class="panel"><h3>Процессор</h3><strong>${d.cpu_percent==null?(d.cpu_sample_available?"ожидается второй замер":"недоступно"):d.cpu_percent.toFixed(1)+" %"}</strong><p>Ядер: ${d.cpu_cores}</p><small>Load 1/5/15 мин: ${d.load.length?d.load.map(v=>v.toFixed(2)).join(" / "):"недоступно"}</small><p class="subtle">Из общего CPU: Pilot ${d.pilot_cpu_percent==null?"ожидается замер":d.pilot_cpu_percent.toFixed(1)+" %"} · nfqws2 ${d.engine_cpu_percent==null?"не измерено":d.engine_cpu_percent.toFixed(1)+" %"}</p><small>Доли от всех ядер суммарно, не от одного ядра. Остальная нагрузка — системные и другие процессы.</small></div><div class="panel"><h3>Память</h3><strong>${d.memory_available==null?"недоступно":prettyBytes(d.memory_total-d.memory_available)+" занято"}</strong><p>Всего: ${prettyBytes(d.memory_total||null)}</p><small>Доступно: ${prettyBytes(d.memory_available)}</small></div><div class="panel"><h3>Температура</h3>${d.temperatures.length?d.temperatures.map(t=>`<p><strong>${t.celsius.toFixed(1)} °C</strong> · ${esc(t.name)}</p>`).join(""):'<p>Недоступно: прошивка не предоставляет датчик Pilot.</p>'}</div></div><div class="panel"><h3>${esc(d.model||"Модель не предоставлена прошивкой")}</h3><p>${esc(d.platform)} · ядро ${esc(d.kernel||"неизвестно")}</p><p>Время работы: ${d.uptime_seconds==null?"недоступно":Math.floor(d.uptime_seconds/86400)+" д "+Math.floor(d.uptime_seconds%86400/3600)+" ч "+Math.floor(d.uptime_seconds%3600/60)+" мин"}</p><p>WAN ${esc(d.wan||"не задан")}: получено ${prettyBytes(d.rx_bytes)}, передано ${prettyBytes(d.tx_bytes)}</p><p>Сейчас: ↓ ${prettyBytes(d.rx_bytes_per_second)}/с · ↑ ${prettyBytes(d.tx_bytes_per_second)}/с</p></div>`;}catch(e){if(target===$("#device-data"))target.textContent="Не удалось получить данные: "+e.message;}finally{deviceLoading=false;loadPreflight();}}
let preflightCached="",preflightUpdated=0,preflightLoading=false;
async function loadPreflight(){
  const el=$("#preflight-data");
  if(!el||document.hidden)return;
  if(preflightCached&&Date.now()-preflightUpdated<60000){if(el.innerHTML!==preflightCached)el.innerHTML=preflightCached;return;}
  if(preflightLoading)return;
  preflightLoading=true;
  const session=panelSession;
  try{
    const d=await api("preflight");
    if(el!==$("#preflight-data")||session!==panelSession||!signedIn)return;
    const other=(d.other_engines||[]);
    const installed=(d.installed_services||[]);
    const free=d.opt_free_bytes==null?"нет замера":prettyBytes(d.opt_free_bytes);
    const processSummary=d.process_list_available
      ?(other.length?"Другие запущенные обработчики: "+other.map(esc).join(", "):"Других известных обработчиков не обнаружено")
      :"Список процессов прочитан не полностью — отсутствие конфликтов не подтверждено";
    el.innerHTML=`<p><strong>${esc(processSummary)}</strong></p>
      <p>Доступно на /opt: ${esc(free)} · тип накопителя: ${esc(d.storage_kind||"неизвестен")}</p>
      <p>Обнаружены установленные службы: ${installed.length?installed.map(esc).join(", "):"не обнаружены"}</p>
      ${(d.notes||[]).map(n=>`<p class="subtle">${esc(n)}</p>`).join("")}
      <small>Наличие файла службы не означает её запуск. Тип накопителя не доказывает, что это внутренняя память.</small>`;
    preflightCached=el.innerHTML;
    preflightUpdated=Date.now();
  }catch(err){if(el===$("#preflight-data")&&session===panelSession&&signedIn)el.textContent="Проверка среды недоступна: "+err.message;}
  finally{preflightLoading=false;}
}

let updateMonitor=null, updateMonitorTimer=null;
const updateStepNames=["Ожидание","Загрузка и проверка","Установка / перезапуск","Проверка версии"];
function updateProgressView(d){
 const titles={waiting:"Ожидание завершения операции",downloading:"Загрузка и проверка пакета",installing:"Установка и перезапуск службы",completed:"Установка завершена",failed:"Установка не завершена",interrupted:"Процесс прерван"};
 const phases={"log-open":"Открытие журнала",waiting:"Завершение текущей операции",prepare:"Подготовка пакета","worker-start":"Запуск установщика",install:"Установка приложения и движка","version-check":"Проверка версии"};
 const bad=d.stage==="failed"||d.stage==="interrupted";
 const confirmed=d.stage==="completed"&&!!d.target&&d.current===d.target;
 const position=confirmed?4:d.stage==="installing"?(d.phase==="version-check"?3:2):d.stage==="downloading"?1:d.stage==="waiting"?0:-1;
 return {title:d.stage==="completed"&&!confirmed?"Установленная версия не подтверждена":titles[d.stage]||"Ожидание статуса",phase:phases[d.phase]||"",position,bad,terminal:bad||d.stage==="completed",confirmed};
}
function ensureUpdateDialog(){
 let dialog=$("#update-monitor");
 if(dialog)return dialog;
 dialog=document.createElement("dialog");
 dialog.id="update-monitor";
 dialog.setAttribute("aria-labelledby","update-monitor-title");
 dialog.innerHTML='<h2 id="update-monitor-title">Обновление Keenetic Pilot</h2><p id="update-monitor-version" class="subtle"></p><p id="update-monitor-state" role="status" aria-live="polite"></p><progress id="update-monitor-progress" aria-label="Выполняется обновление"></progress><ol id="update-monitor-steps"></ol><p id="update-monitor-detail" class="subtle"></p><div class="form-actions"><button id="update-monitor-copy" type="button">Скачать диагностику</button><button id="update-monitor-close" type="button">Свернуть</button></div><small>Закрытие окна не отменяет обновление. Не отключайте питание роутера во время установки.</small>';
 document.body.appendChild(dialog);
 $("#update-monitor-close").onclick=()=>dialog.close();
 // A backdrop click minimizes the monitor only; polling and installation continue.
 let pressedOutside=false;
 const outside=e=>{const r=dialog.getBoundingClientRect();return e.target===dialog&&(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom);};
 dialog.addEventListener("pointerdown",e=>{pressedOutside=e.button===0&&outside(e);});
 dialog.addEventListener("pointercancel",()=>{pressedOutside=false;});
 dialog.addEventListener("click",e=>{const dismiss=pressedOutside&&outside(e);pressedOutside=false;if(dismiss&&dialog.open)dialog.close();});
 dialog.addEventListener("close",()=>{pressedOutside=false;});
 $("#update-monitor-copy").onclick=()=>{
  if(!updateMonitor)return;
  const m=updateMonitor,d=m.last||{};
  const report={target:m.target,source:m.source,current:d.current||null,stage:d.stage||null,phase:d.phase||null,exit_code:d.exit_code||null,connection:m.connection,checked_at:m.checkedAt||null};
  const url=URL.createObjectURL(new Blob([JSON.stringify(report,null,2)],{type:"application/json"}));
  const a=document.createElement("a");a.href=url;a.download="pilot-update-status.json";a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
 };
 return dialog;
}
function paintUpdateMonitor(){
 const m=updateMonitor;if(!m)return;
 ensureUpdateDialog();
 const d=m.last||{},v=updateProgressView(d);
 $("#update-monitor-version").textContent=(m.source?m.source+" · ":"")+"Цель: "+(m.target||d.target||"не определена")+(d.current?" · Установлено: "+d.current:"");
 $("#update-monitor-state").textContent=m.rejected?"Запрос установки отклонён":m.connection==="lost"?"Нет связи с панелью. Ожидаем восстановления; итог ещё неизвестен.":m.pending?"Запрос установки отправляется…":v.title;
 $("#update-monitor-progress").hidden=m.rejected||(!m.pending&&v.terminal);
 $("#update-monitor-detail").textContent=[v.phase,d.exit_code?"Код завершения: "+d.exit_code:"",m.rejected?"Запрос не принят. Проверь сообщение панели.":"",v.bad?"Проверь состояние службы. Автоматический откат не подтверждён.":"",m.checkedAt?"Последний ответ: "+new Date(m.checkedAt).toLocaleTimeString("ru-RU"):""].filter(Boolean).join(" · ");
 const steps=$("#update-monitor-steps");steps.replaceChildren();
 updateStepNames.forEach((name,i)=>{const li=document.createElement("li");li.textContent=(v.bad?"— ":i<v.position?"✓ ":i===v.position?"▶ ":"○ ")+name;steps.appendChild(li);});
 $("#update-monitor-close").textContent=v.terminal||m.rejected?"Закрыть":"Свернуть";
}
function stopUpdateMonitor(){
 clearTimeout(updateMonitorTimer);updateMonitorTimer=null;updateMonitor=null;
 const dialog=$("#update-monitor");if(dialog?.open)dialog.close();
}
function openUpdateMonitor(target="",source=""){
 if(!updateMonitor||target&&updateMonitor.target!==target){
  clearTimeout(updateMonitorTimer);
  updateMonitor={target,source,last:null,pending:false,rejected:false,connection:"unknown",checkedAt:null,delay:3000};
 }
 const dialog=ensureUpdateDialog();
 paintUpdateMonitor();if(!dialog.open)dialog.showModal();
 scheduleUpdateMonitor(0);
}
function scheduleUpdateMonitor(delay){
 clearTimeout(updateMonitorTimer);
 if(updateMonitor&&!updateMonitor.rejected)updateMonitorTimer=setTimeout(pollUpdateMonitor,delay);
}
async function pollUpdateMonitor(){
 const m=updateMonitor;if(!m||!signedIn||m.polling)return;
 m.polling=true;
 const session=panelSession;
 try{
  const d=await api("updates");
  if(updateMonitor!==m||session!==panelSession||!signedIn)return;
  m.connection="ok";m.checkedAt=Date.now();m.delay=3000;
  if(!m.target&&d.target)m.target=d.target;
  if(!m.target||d.target===m.target)m.last=d;
  paintUpdateMonitor();
 }catch(e){
  if(updateMonitor!==m||session!==panelSession||!signedIn)return;
  m.connection="lost";m.delay=Math.min(15000,m.delay*2);paintUpdateMonitor();
 }finally{
  m.polling=false;
  if(updateMonitor===m&&signedIn&&!m.rejected&&(m.pending||m.connection==="lost"||!m.last||m.last.running||!updateProgressView(m.last).terminal)){
   if(!m.target&&m.last?.stage==="idle"&&!m.pending)return;
   scheduleUpdateMonitor(m.delay);
  }
 }
}
async function startMonitoredUpdate(path,payload,source,button){
 if(updateMonitor?.pending)return;
 button.disabled=true;
 openUpdateMonitor(payload.version,source);
 const m=updateMonitor;m.pending=true;m.rejected=false;m.last=null;paintUpdateMonitor();
 try{
  await api(path,"POST",payload);
  await Promise.all([loadUpdates(true),loadSourceUpdates(true)]);
 }catch(e){
  if(updateMonitor!==m)return;
  if(e.status>=400&&e.status<500){m.rejected=true;notify(e.message,true);}
  else{m.connection="lost";notify("Ответ на запуск не получен; проверяем состояние.",true);}
 }finally{
  if(updateMonitor===m){m.pending=false;paintUpdateMonitor();if(!m.rejected)scheduleUpdateMonitor(0);}
  if(button.isConnected)button.disabled=m.rejected?false:Boolean(updatesData?.running||sourceUpdatesData?.running||!m.last||!updateProgressView(m.last).terminal);
 }
}

function updatesPage(){return heading("Обновления","Обновление Pilot, панели и закреплённого nfqws2 с сохранением настроек.")+`<div class="panel"><h3>Установленная версия</h3><div id="installed-release-notes">${releaseNotesHTML(state.version,"installed",true)}</div></div><div class="panel"><div id="update-info">Загрузка…</div><div class="form-actions"><button id="update-check">Проверить версию</button><button id="update-install" class="primary" disabled>Установить обновление</button><button id="update-monitor-open" type="button">Ход обновления</button></div><p class="subtle">Выпуск проверяется по подписи и SHA-256. Перед остановкой службы проверяются пакет и конфигурация; установщик сохраняет прежнюю версию для отката при ошибке. Текущий подбор будет отменён перед установкой. Будет краткий перерыв. После перезапуска войдите с прежним паролем.</p></div><div class="panel"><h3>ChatGPT / Codex · отдельные обновления</h3><p class="subtle">Это дополнительный канал GitHub HTTPS + SHA-256, без независимой подписи Ed25519. Автор указан издателем в GitHub, а не подтверждён криптографически. Старый подписанный канал выше оставлен без изменений.</p><div id="source-update-info">Загрузка сведений об источниках…</div></div><div class="panel"><h3>Каталог методов</h3><p>Подписанные стратегии можно загрузить отдельно от приложения.</p><button id="catalog-update">Загрузить стратегии сейчас</button></div>`;}
async function loadSourceUpdates(force=false){
  const target=$("#source-update-info");
  if(!target||(sourceUpdatesLoading&&!force))return;
  sourceUpdatesLoading=true;
  const serial=++sourceUpdatesSerial;
  try{
    const d=await api("updates/sources");
    if(target!==$("#source-update-info")||serial!==sourceUpdatesSerial)return;
    sourceUpdatesData=d;
    rememberReleaseNotes(target);
    target.innerHTML=(d.sources||[]).map(item=>{
      const src=esc(item.source);
      if(item.checked){lastSourceChecks.set(item.source,{version:item.latest,commit:item.commit});}
      const previous=lastSourceChecks.get(item.source);
      const latest=item.checked?esc(item.latest):"Не проверено";
      const status=!item.checked?"Для поиска нажмите «Проверить»":item.available?"Есть новая версия":"Новых выпусков нет";
      return `<div class="source-update-row" data-source-row="${src}">
        <div class="panel-head"><strong>${src}</strong><span>${esc(status)}</span></div>
        <p>Версия: ${latest}${item.checked&&item.commit?" · коммит "+esc(item.commit.slice(0,8)):""}</p>
        ${releaseNotesHTML(item.checked?item.latest:previous?.version,item.source)}
        ${!item.checked&&previous?`<p>Последний подтверждённый ранее выпуск: ${esc(previous.version)} · ${esc(previous.commit?.slice(0,8))}. Установка недоступна до новой проверки.</p>`:""}
        ${sourceCheckErrors.has(item.source)?`<p class="fail-text">${esc(sourceCheckErrors.get(item.source))}</p>`:""}
        <div class="form-actions">
          <button data-source-update-check="${src}" ${d.running?"disabled":""}>Проверить ${src}</button>
          <button data-source-update-install="${src}" ${!d.supported||!item.available||d.running?"disabled":""}>Установить ${latest} [${src}]</button>
        </div>
      </div>`;
    }).join("");
    target.querySelectorAll("[data-source-update-check]").forEach(button=>button.addEventListener("click",async()=>{
      button.disabled=true;
      try{await api("updates/sources/check","POST",{source:button.dataset.sourceUpdateCheck});sourceCheckErrors.delete(button.dataset.sourceUpdateCheck);await loadSourceUpdates(true);}
      catch(err){sourceCheckErrors.set(button.dataset.sourceUpdateCheck,err.message);notify("Ошибка проверки: "+err.message,true);await loadSourceUpdates(true);button.disabled=false;}
    }));
    target.querySelectorAll("[data-source-update-install]").forEach(button=>button.addEventListener("click",async()=>{
      const source=button.dataset.sourceUpdateInstall;
      const item=sourceUpdatesData?.sources?.find(x=>x.source===source);
      if(!item?.available||!item.latest)return;
      if(!window.confirm("Установить "+item.latest+" ["+source+"] из GitHub? Проверка HTTPS + SHA-256; НЕ независимая подпись. Перед перезапуском проверяются пакет и конфигурация."))return;
      button.disabled=true;
      await startMonitoredUpdate("updates/sources/install",{source,version:item.latest},source,button);
    }));
  }catch(e){if(target===$("#source-update-info"))target.textContent="Не удалось получить сведения об источниках: "+e.message;}
  finally{if(serial===sourceUpdatesSerial)sourceUpdatesLoading=false;}
}
async function loadUpdates(force=false){const target=$("#update-info");if(!target||(updatesLoading&&!force))return;updatesLoading=true;const serial=++updatesSerial;try{const d=await api("updates");if(target!==$("#update-info")||serial!==updatesSerial)return;updatesData=d;const installed=$("#installed-release-notes");if(installed){const key="installed:"+String(d.current||"unknown");if(installed.querySelector("details")?.dataset.releaseNotes!==key){rememberReleaseNotes(installed);installed.innerHTML=releaseNotesHTML(d.current,"installed",true);}}const stages={idle:"Готово к проверке",downloading:"Загрузка и проверка пакета",installing:"Установка и перезапуск службы",waiting:"Ожидание завершения текущего подбора перед обновлением",completed:"Обновление завершено",failed:"Обновление не завершено. Подробности — в /opt/etc/keenetic-pilot/update.log; проверьте состояние службы.",interrupted:"Процесс обновления прерван; проверьте состояние службы перед повтором."};target.innerHTML=`<h3>Pilot ${esc(d.current)}</h3><p>nfqws2: ${esc(d.engine||"версия не получена")}</p><p>${esc(stages[d.stage]||d.stage)}</p>${d.stage==="failed"||d.stage==="interrupted"?`<p>${d.phase?"Этап: "+esc(({"log-open":"открытие журнала",prepare:"подготовка пакета",waiting:"завершение текущей операции","worker-start":"запуск установки",install:"установка приложения и движка","version-check":"проверка установленной версии"})[d.phase]||"неизвестен"):""}${d.exit_code?" · код завершения: "+esc(d.exit_code):""}</p>`:""}<p>${d.latest?"Последний проверенный выпуск: "+esc(d.latest)+(d.latest_engine?" · nfqws2 "+esc(d.latest_engine):""):"Проверка новых выпусков ещё не выполнялась"}</p>${!d.supported?'<p class="subtle">Установка из панели доступна для управляемой установки на роутере.</p>':""}`;$("#update-install").disabled=!d.supported||!d.available||d.running;$("#update-install").textContent=d.available?"Установить "+d.latest:"Установить обновление";$("#update-check").disabled=d.running||d.checking;}catch(e){if(target===$("#update-info"))target.textContent="Связь с панелью временно недоступна: "+e.message;}finally{if(serial===updatesSerial)updatesLoading=false;}}
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
  const session = panelSession;
  await refresh();
  if (session !== panelSession) return;
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
let polling = false, pollDelay = 4000, pollTimer;
function schedulePoll() {
  clearTimeout(pollTimer);
  pollTimer = setTimeout(pollStatus, pollDelay);
}
async function pollStatus() {
  if (polling) return;
  if (!signedIn || document.hidden || statusReads) {
    schedulePoll();
    return;
  }
  polling = true;
  try {
    await refresh(false, true);
    pollDelay = 4000;
  } catch {
    pollDelay = Math.min(32000, pollDelay * 2);
  } finally {
    polling = false;
    schedulePoll();
  }
}
$("#retry-connection").addEventListener("click", pollStatus);
document.addEventListener("visibilitychange", () => { if (!document.hidden) pollStatus(); });
schedulePoll();

(function initPilotTheme(){
 const select=$("#pilot-theme");if(!select)return;
 const root=document.documentElement,fields=["accent","bg","panel","text"];
 const defaults={theme:"matrix",accent:"#57f68b",bg:"#101318",panel:"#1b2028",text:"#e2f3e8"};
 let prefs={...defaults};
 try{const saved=JSON.parse(localStorage.getItem("pilot-theme-v1")||"{}");if(saved&&typeof saved==="object"){if(["matrix","dark","light","aero","luna","custom"].includes(saved.theme))prefs.theme=saved.theme;for(const k of fields)if(/^#[0-9a-f]{6}$/i.test(saved[k]))prefs[k]=saved[k];}}catch{}
 function apply(){
  select.value=prefs.theme;root.dataset.pilotTheme=prefs.theme;$("#theme-custom").hidden=prefs.theme!=="custom";
  for(const k of fields){$("#theme-"+k).value=prefs[k];if(prefs.theme==="custom")root.style.setProperty("--"+k,prefs[k]);else root.style.removeProperty("--"+k);}
  try{localStorage.setItem("pilot-theme-v1",JSON.stringify(prefs));}catch{}
 }
 select.addEventListener("change",()=>{prefs.theme=select.value;apply();});
 for(const k of fields)$("#theme-"+k).addEventListener("input",e=>{prefs[k]=e.target.value;apply();});
 $("#theme-reset").addEventListener("click",()=>{prefs={...defaults};apply();});
 apply();
})();

 // Close only transient header popovers, not inline details or editable forms.
 const transientPopovers=()=>document.querySelectorAll("#pilot-music[open],.theme-picker[open]");
 document.addEventListener("click",e=>{
  if(!(e.target instanceof Node))return;
  for(const panel of transientPopovers())if(!panel.contains(e.target))panel.open=false;
 });
 document.addEventListener("keydown",e=>{
  if(e.key!=="Escape"||e.defaultPrevented||document.querySelector("dialog[open]"))return;
  for(const panel of transientPopovers()){
   const restore=panel.contains(document.activeElement);
   panel.open=false;
   if(restore)panel.querySelector("summary")?.focus();
  }
 });

function renderNotifications(){
 const target=$("#notification-center");if(!target)return;
 target.innerHTML=panelNotifications.length?'<ul>'+panelNotifications.map(n=>'<li>'+esc(n.at)+' · '+(n.error?'Ошибка: ':'')+esc(n.message)+'</li>').join("")+'</ul>':'<p>Уведомлений пока нет.</p>';
}
function bindSettingsBackup(){
 $("#settings-backup-download")?.addEventListener("click",()=>action(async()=>{
  const data=await api("settings-backup");const url=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:"application/json"}));
  const a=document.createElement("a");a.href=url;a.download="pilot-settings-backup.json";a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
 }));
 $("#settings-backup-restore")?.addEventListener("click",()=>action(async()=>{
  const file=$("#settings-backup-file")?.files[0];if(!file||file.size>65536)throw Error("Выберите резервную копию до 64 КБ.");
  const data=JSON.parse(await file.text());
  if(!window.confirm("Восстановить настройки из "+file.name+"? Текущая конфигурация будет сохранена в config.json.previous. Возможен перезапуск движка."))return;
  await api("settings-backup/restore","POST",data);notify("Настройки восстановлены");
 }));
}
