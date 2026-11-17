"use strict";

// Keenetic Pilot mini-player. Audio stays in the browser, NEVER on the router.
// No autoplay, no upload, no background polling and no bundled copyrighted track.
(function () {
  const byId = (id) => document.getElementById(id);
  const music = byId("pilot-music");
  const audio = byId("music-audio");
  if (!music || !audio) return;

  const fileInput = byId("music-file");
  const urlInput = byId("music-url");
  const urlButton = byId("music-load-url");
  const resetButton = byId("music-reset");
  const repeatInput = byId("music-repeat");
  const sleepInput = byId("music-sleep");
  const message = byId("music-message");
  const title = byId("music-title");
  const summaryText = byId("music-summary-label");
  let objectURL = "";
  let sleepTimer = null;
  let trackName = "";

  function say(value, isError) {
    message.textContent = value;
    message.classList.toggle("music-error", Boolean(isError));
  }

  function cancelTimer() {
    if (sleepTimer !== null) {
      clearTimeout(sleepTimer);
      sleepTimer = null;
    }
  }

  function armTimer() {
    cancelTimer();
    const minutes = Number(sleepInput.value);
    if (minutes > 0 && !audio.paused && audio.currentSrc) {
      sleepTimer = setTimeout(() => {
        sleepTimer = null;
        audio.pause();
        sleepInput.value = "0";
        say("Таймер завершён — музыка остановлена.");
      }, minutes * 60000);
    }
  }

  function revokeLocal() {
    if (objectURL) {
      URL.revokeObjectURL(objectURL);
      objectURL = "";
    }
  }

  function stopAndClear() {
    cancelTimer();
    audio.pause();
    audio.removeAttribute("src");
    audio.load();
    revokeLocal();
    fileInput.value = "";
    urlInput.value = "";
    sleepInput.value = "0";
    trackName = "";
    title.textContent = "Нет выбранной песни";
    summaryText.textContent = "♫ Музыка";
    say("Выбери песню с устройства или укажи прямую HTTPS-ссылку.");
  }

  function selectTrack(source, name, localFile) {
    cancelTimer();
    audio.pause();
    audio.removeAttribute("src");
    audio.load(); // Release the previous resource before revoking an object URL.
    revokeLocal();
    if (localFile) objectURL = source;
    trackName = String(name || "Аудио").slice(0, 120);
    title.textContent = trackName;
    audio.loop = repeatInput.checked;
    audio.src = source;
    audio.load(); // User explicitly selected a file or clicked "Open HTTPS".
    summaryText.textContent = "♫ Музыка";
    say("Песня загружена. Для запуска нажми ▶ в плеере.");
  }

  fileInput.addEventListener("change", () => {
    const file = fileInput.files && fileInput.files[0];
    if (!file) return;
    if (file.size > 250 * 1024 * 1024 ||
        !(file.type.startsWith("audio/") ||
          /\.(mp3|m4a|aac|ogg|oga|wav|flac|webm)$/i.test(file.name))) {
      fileInput.value = "";
      say("Нужен аудиофайл MP3, M4A, OGG, WAV, FLAC или WebM размером до 250 МБ.", true);
      return;
    }
    const url = URL.createObjectURL(file);
    selectTrack(url, file.name, true);
  });

  urlButton.addEventListener("click", () => {
    const input = urlInput.value.trim();
    let source;
    try {
      source = new URL(input);
      if (input.length > 2048 || source.protocol !== "https:" ||
          source.username || source.password || !source.hostname) {
        throw new Error("bad-url");
      }
    } catch (_) {
      say("Укажи прямую HTTPS-ссылку на аудио без логина и пароля.", true);
      return;
    }
    const filename = source.pathname.split("/").pop() || source.hostname;
    fileInput.value = "";
    selectTrack(source.href, filename, false);
  });

  repeatInput.addEventListener("change", () => {
    audio.loop = repeatInput.checked;
  });
  sleepInput.addEventListener("change", armTimer);
  resetButton.addEventListener("click", stopAndClear);

  audio.addEventListener("play", () => {
    summaryText.textContent = "♫ Играет";
    say("Сейчас играет: " + trackName);
    armTimer();
  });
  audio.addEventListener("pause", () => {
    cancelTimer();
    if (trackName && !audio.ended) {
      summaryText.textContent = "♫ Пауза";
      say("На паузе: " + trackName);
    }
  });
  audio.addEventListener("ended", () => {
    cancelTimer();
    summaryText.textContent = "♫ Музыка";
    say("Песня закончилась.");
  });
  audio.addEventListener("error", () => {
    cancelTimer();
    say("Браузер не смог открыть аудио. Проверь файл, ссылку или формат.", true);
  });

  // Leave the browser UI and router state clean after logout or navigation away.
  document.addEventListener("pilot-logout", stopAndClear);
  window.addEventListener("pagehide", () => {
    audio.pause();
    revokeLocal();
  });
})();
