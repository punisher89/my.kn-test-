#!/usr/bin/env node
"use strict";
const assert=require("node:assert/strict"),fs=require("node:fs"),path=require("node:path"),vm=require("node:vm");
function element(){const events={};return {value:"",checked:false,src:"",paused:true,textContent:"",classList:{toggle(){}},setAttribute(){},addEventListener(n,f){events[n]=f;},emit(n){return events[n]?.();},replaceChildren(...options){this.options=options;this.value=options[0]?.value||"";},pause(){this.paused=true;},removeAttribute(){this.src="";},load(){},play(){this.paused=false;this.emit("playing");return Promise.resolve();}};}
const nodes={},get=id=>nodes[id]??=element(),events={},windowEvents={},data=new Map(),timers=[];
const document={getElementById:get,createElement:()=>element(),addEventListener:(n,f)=>events[n]=f};
const window={addEventListener:(n,f)=>windowEvents[n]=f};
vm.runInNewContext(fs.readFileSync(path.resolve(__dirname,"../internal/pilot/web/music.js"),"utf8"),{document,window,URL,localStorage:{getItem:k=>data.get(k)||null,setItem:(k,v)=>data.set(k,v)},setTimeout:(f,ms)=>{const t={f,ms};timers.push(t);return t;},clearTimeout:()=>{}});
const audio=get("music-audio");
assert.equal(audio.src,"");assert.equal(audio.paused,true);assert.equal(get("radio-station").options.length,24);
get("radio-search").value="Москва";get("radio-search").emit("input");assert.equal(get("radio-station").options.length,14);
get("radio-search").value="максимум";get("radio-search").emit("input");assert.equal(get("radio-station").options.length,1);assert.equal(get("radio-station").value,"maximum");assert.equal(audio.src,"");
get("radio-play").emit("click");assert.equal(audio.src,"https://maximum.hostingradio.ru/maximum128.mp3");get("music-reset").emit("click");assert.equal(audio.src,"");
get("radio-search").value="trance";get("radio-search").emit("input");assert.equal(get("radio-station").options.length,1);assert.equal(audio.src,"");
get("radio-favorite").emit("click");assert.match(data.get("pilot-radio-favorites"),/trance/);
get("radio-search").value="";get("radio-search").emit("input");get("radio-only-favorites").checked=true;get("radio-only-favorites").emit("change");assert.equal(get("radio-station").options.length,1);
get("music-sleep").value="15";get("radio-play").emit("click");assert.match(audio.src,/tm96/);assert.equal(timers.at(-1).ms,900000);timers.at(-1).f();assert.equal(audio.src,"");assert.equal(audio.paused,true);
for(const url of ["http://example.com/radio","javascript:alert(1)","https://user:pass@example.com/radio"]){get("music-url").value=url;get("music-load-url").emit("click");assert.equal(audio.src,"");}
get("music-url").value="https://example.com/radio";get("music-load-url").emit("click");assert.equal(audio.src,"https://example.com/radio");
get("radio-volume").value="25";get("radio-volume").emit("input");assert.equal(audio.volume,.25);assert.equal(data.get("pilot-radio-volume"),"25");
events["pilot-logout"]();assert.equal(audio.src,"");assert.equal(get("music-url").value,"");assert.equal(audio.paused,true);
console.log("PASS: radio catalog, search, favorites, volume, HTTPS validation, explicit play, sleep and logout");
