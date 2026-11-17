#!/usr/bin/env node
"use strict";
const assert=require("node:assert/strict");
const fs=require("node:fs");
const path=require("node:path");
const vm=require("node:vm");

function element(){
  const events={};
  return {
    value:"",checked:false,files:[],textContent:"",currentSrc:"",src:"",
    paused:true,ended:false,loop:false,
    classList:{toggle(){},add(){},remove(){}},
    addEventListener(name,fn){(events[name] ||= []).push(fn);},
    emit(name){for(const f of events[name]||[]) f({target:this});},
    removeAttribute(name){if(name==="src"){this.src="";this.currentSrc="";}},
    pause(){this.paused=true;this.emit("pause");},
    load(){this.currentSrc=this.src;},
    play(){this.paused=false;this.emit("play");}
  };
}
const ids=["pilot-music","music-audio","music-file","music-url","music-load-url",
  "music-reset","music-repeat","music-sleep","music-message","music-title","music-summary-label"];
const nodes=Object.fromEntries(ids.map(id=>[id,element()]));
const docEvents={},windowEvents={};
let blobCounter=0;
const revoked=[], timers=[];
class MediaURL extends URL {}
MediaURL.createObjectURL=()=> "blob:music-test/"+(++blobCounter);
MediaURL.revokeObjectURL=url=>revoked.push(url);
const document={
  getElementById:id=>nodes[id],
  addEventListener:(name,fn)=>{docEvents[name]=fn;}
};
const window={addEventListener:(name,fn)=>{windowEvents[name]=fn;}};
const script=fs.readFileSync(path.resolve(__dirname,"../internal/pilot/web/music.js"),"utf8");
vm.runInNewContext(script,{
  document,window,URL:MediaURL,
  setTimeout:(fn,delay)=>{const obj={fn,delay,cancelled:false};timers.push(obj);return obj;},
  clearTimeout:id=>{id.cancelled=true;}
},{filename:"music.js"});
const audio=nodes["music-audio"];
assert.equal(audio.src,"","player must never start downloading a song on page load");
assert.equal(audio.paused,true,"autoplay forbidden");
assert.equal(nodes["music-summary-label"].textContent,"","player should not override label before track selection");

nodes["music-url"].value="http://example.com/song.mp3";
nodes["music-load-url"].emit("click");
assert.equal(audio.src,"","insecure HTTP music must not be loaded");
nodes["music-url"].value="javascript:alert(1)";
nodes["music-load-url"].emit("click");
assert.equal(audio.src,"","javascript URL must not be accepted");
nodes["music-url"].value="https://user:secret@media.example.com/song.mp3";
nodes["music-load-url"].emit("click");
assert.equal(audio.src,"","credential URLs must not be accepted");

nodes["music-url"].value="https://media.example.com/music.mp3";
nodes["music-load-url"].emit("click");
assert.equal(audio.src,"https://media.example.com/music.mp3");
assert.equal(audio.paused,true,"opening music must not start playback");
assert.match(nodes["music-message"].textContent,/нажми/);

nodes["music-repeat"].checked=true;
nodes["music-repeat"].emit("change");
assert.equal(audio.loop,true);

nodes["music-sleep"].value="15";
nodes["music-sleep"].emit("change");
assert.equal(timers.length,0,"sleep timer must not run while audio is paused");
audio.play();
assert.equal(timers.length,1);
assert.equal(timers[0].delay,15*60000);
assert.match(nodes["music-summary-label"].textContent,/Играет/);
timers[0].fn();
assert.equal(audio.paused,true);
assert.equal(nodes["music-sleep"].value,"0");

nodes["music-file"].files=[{name:"Тестовая песня.mp3",type:"audio/mpeg",size:1024}];
nodes["music-file"].emit("change");
assert.equal(audio.src,"blob:music-test/1");
assert.match(nodes["music-title"].textContent,/Тестовая песня/);
assert.equal(audio.paused,true);
nodes["music-file"].files=[{name:"Другая.ogg",type:"audio/ogg",size:2048}];
nodes["music-file"].emit("change");
assert.equal(audio.src,"blob:music-test/2");
assert.deepEqual(revoked,["blob:music-test/1"],"old object URL must be revoked");

nodes["music-file"].files=[{name:"not-a-track.exe",type:"application/octet-stream",size:512}];
nodes["music-file"].emit("change");
assert.equal(audio.src,"blob:music-test/2","invalid files must not replace song");
assert.match(nodes["music-message"].textContent,/аудиофайл/);

assert.equal(typeof docEvents["pilot-logout"],"function");
docEvents["pilot-logout"]();
assert.equal(audio.src,"");
assert.equal(audio.paused,true);
assert.deepEqual(revoked,["blob:music-test/1","blob:music-test/2"]);
assert.equal(nodes["music-title"].textContent,"Нет выбранной песни");
assert.equal(typeof windowEvents.pagehide,"function");
console.log("PASS: music player requires user action, validates URLs/files, loops, stops on timer/logout, never uploads to router");
