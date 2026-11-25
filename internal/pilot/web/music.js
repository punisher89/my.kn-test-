"use strict";
// Browser-only live radio: no router RPC, upload, proxy, external catalog or autoplay.
(function(){
  const byId=id=>document.getElementById(id);
  const audio=byId("music-audio"), select=byId("radio-station");
  if(!audio||!select)return;
  const search=byId("radio-search"), favorite=byId("radio-favorite"), only=byId("radio-only-favorites"),
    volume=byId("radio-volume"), sleep=byId("music-sleep"), title=byId("music-title"),
    label=byId("music-summary-label"), message=byId("music-message"), custom=byId("music-url");
  // Public direct HTTPS streams; catalog is local, no requests before explicit play.
  const stations=[
    ["maximum","Радио MAXIMUM","Москва рок alternative максимум","https://maximum.hostingradio.ru/maximum128.mp3"],
    ["nashe","Наше Радио","Москва русский рок","https://nashe1.hostingradio.ru/nashe-128.mp3"],
    ["rusradio","Русское Радио","Москва поп русская музыка","https://rusradio.hostingradio.ru/rusradio128.mp3"],
    ["dfm","DFM","Москва танцевальная dance","https://dfm.hostingradio.ru/dfm96.aacp"],
    ["montecarlo","Радио Monte Carlo","Москва lounge поп джаз монте карло","https://montecarlo.hostingradio.ru/montecarlo96.aacp"],
    ["hitfm","Хит FM","Москва поп хиты","https://hitfm.hostingradio.ru/hitfm96.aacp"],
    ["europa","Европа Плюс","Москва поп хиты","https://ep256.hostingradio.ru:8052/europaplus256.mp3"],
    ["retrofm","Ретро FM","Москва ретро хиты","https://retro.hostingradio.ru:8014/retro320.mp3"],
    ["avtoradio","Авторадио","Москва поп ретро","https://srv21.gpmradio.ru:8443/stream/air/aac/64/100"],
    ["mayak","Радио Маяк","Москва разговорное музыка","https://icecast-vgtrk.cdnvideo.ru/mayakfm_aac_64kbps"],
    ["govoritmoskva","Говорит Москва","Москва новости разговорное","https://media.govoritmoskva.ru/rufm.mp3"],
    ["orpheus","Радио Орфей","Москва классическая музыка","https://orfeyfm.hostingradio.ru:8034/orfeyfm128.mp3"],
    ["chocolate","Радио Шоколад","Москва каверы поп","https://choco.hostingradio.ru:10010/fm"],
    ["businessfm","Business FM","Москва бизнес новости","https://bfm.hostingradio.ru:9075/fm"],
    ["record","Радио Record","dance электронная","https://radiorecord.hostingradio.ru/rr_main96.aacp"],
    ["gold","Record Gold","dance ретро","https://radiorecord.hostingradio.ru/gold96.aacp"],
    ["eurodance","Record Eurodance","dance ретро","https://radiorecord.hostingradio.ru/eurodance96.aacp"],
    ["trance","Record Trancemission","trance электронная","https://radiorecord.hostingradio.ru/tm96.aacp"],
    ["lofi","Record Lo-Fi","chill спокойная","https://radiorecord.hostingradio.ru/lofi96.aacp"],
    ["synth","Record Synthwave","synth ретро","https://radiorecord.hostingradio.ru/synth96.aacp"],
    ["groove","SomaFM Groove Salad","chill downtempo","https://ice5.somafm.com/groovesalad-128-aac"],
    ["drone","SomaFM Drone Zone","ambient спокойная","https://ice5.somafm.com/dronezone-128-aac"],
    ["classic","SomaFM Groove Salad Classic","chill downtempo","https://ice5.somafm.com/gsclassic-128-aac"],
    ["paradise","Radio Paradise","рок rock eclectic","https://stream.radioparadise.com/mp3-128"]
  ];
  function read(key,fallback){try{return JSON.parse(localStorage.getItem("pilot-radio-"+key))??fallback;}catch(_){return fallback;}}
  function save(key,value){try{localStorage.setItem("pilot-radio-"+key,JSON.stringify(value));}catch(_){}}
  const stored=read("favorites",[]);
  const favorites=new Set(Array.isArray(stored)?stored.filter(id=>stations.some(s=>s[0]===id)):[]);
  let timer=null, generation=0, name="";
  const savedVolume=read("volume",65);
  volume.value=String(typeof savedVolume==="number"&&Number.isFinite(savedVolume)?Math.min(100,Math.max(0,savedVolume)):65);
  audio.volume=Number(volume.value)/100;
  function say(text,error=false){message.textContent=text;message.classList.toggle("music-error",error);}
  function current(){return stations.find(s=>s[0]===select.value);}
  function mark(){const yes=favorites.has(select.value);favorite.disabled=!current();favorite.textContent=yes?"★ Из избранного":"☆ В избранное";favorite.setAttribute("aria-pressed",String(yes));byId("radio-play").disabled=!current();}
  function render(){
    const previous=select.value, q=search.value.trim().toLowerCase();
    const found=stations.filter(s=>(!only.checked||favorites.has(s[0]))&& (s[1]+" "+s[2]).toLowerCase().includes(q));
    select.replaceChildren(...found.map(s=>{const o=document.createElement("option");o.value=s[0];o.textContent=s[1]+" · "+s[2];return o;}));
    if(found.some(s=>s[0]===previous))select.value=previous;
    mark();
  }
  function cancel(){if(timer!==null){clearTimeout(timer);timer=null;}}
  function stop(text="Эфир остановлен."){
    generation++;cancel();name="";audio.pause();audio.removeAttribute("src");audio.load();
    title.textContent="Станция не выбрана";label.textContent="♫ Радио";say(text);
  }
  function arm(){cancel();const minutes=Number(sleep.value);if([15,30,60].includes(minutes)&&name&&!audio.paused){
    timer=setTimeout(()=>{sleep.value="0";stop("Таймер завершён — эфир остановлен.");},minutes*60000);
  }}
  async function play(url,stationName){
    stop();const attempt=generation;name=stationName;title.textContent=name;label.textContent="♫ Подключение";say("Подключение к эфиру…");
    audio.src=url;
    try{await audio.play();if(attempt!==generation)return;}
    catch(_){if(attempt===generation){stop("Не удалось открыть эфир. Проверь сеть или выбери другую станцию.");message.classList.toggle("music-error",true);}}
  }
  search.addEventListener("input",render);only.addEventListener("change",render);select.addEventListener("change",mark);
  favorite.addEventListener("click",()=>{const s=current();if(!s)return;if(favorites.has(s[0]))favorites.delete(s[0]);else favorites.add(s[0]);save("favorites",[...favorites]);render();});
  byId("radio-play").addEventListener("click",()=>{const s=current();if(s)play(s[3],s[1]);});
  byId("music-load-url").addEventListener("click",()=>{
    try{const raw=custom.value.trim(),u=new URL(raw);if(raw.length>2048||u.protocol!=="https:"||u.username||u.password||!u.hostname)throw Error();play(u.href,u.hostname);}
    catch(_){say("Нужна прямая HTTPS-ссылка без логина и пароля.",true);}
  });
  volume.addEventListener("input",()=>{audio.volume=Number(volume.value)/100;save("volume",Number(volume.value));});
  sleep.addEventListener("change",arm);
  byId("music-reset").addEventListener("click",()=>stop());
  audio.addEventListener("playing",()=>{if(name&&!audio.paused){label.textContent="♫ Эфир";say("В эфире: "+name);if(timer===null)arm();}});
  audio.addEventListener("error",()=>{if(name){stop("Поток недоступен или формат не поддерживается.");message.classList.toggle("music-error",true);}});
  audio.addEventListener("ended",()=>{if(name)stop("Вещание завершилось. Нажми «Слушать» для повторного подключения.");});
  document.addEventListener("pilot-logout",()=>{stop();custom.value="";sleep.value="0";});
  window.addEventListener("pagehide",()=>stop());
  render();
})();
