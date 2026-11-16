-- Pilot's isolated controller probes and verified domain routes. No commands,
-- Lua code or user URLs are loaded from these bounded root-owned data files.
local routes,read_at={},-1
local function fields(line)
    local row={};for value in (line..'\t'):gmatch('(.-)\t') do row[#row+1]=value end;return row
end
local function host_ok(s) return type(s)=='string' and #s<=253 and s:match('^[a-z0-9][a-z0-9%.%-]*%.[a-z][a-z0-9%-]*$') and not s:find('%.%.') end
local function id_ok(s) return type(s)=='string' and #s<=96 and s:match('^[a-z0-9_-]+$') end
local function scope_ok(tls,family,port)
    return (tls=='tls12' or tls=='tls13') and (family=='ipv4' or family=='ipv6') and (port==443 or port==2053 or port==2083 or port==2087 or port==2096 or port==8443)
end
local function route_key(host,tls,family,port) return host..'/'..tls..'/'..family..'/'..port end
function pilot_verified(host,tls,family,port)
    local now=os.time()
    if now~=read_at then
        read_at=now;local next_routes={};local f=pilot_runtime and io.open(pilot_runtime..'/verified-domains.tsv','r')
        if f then
            if f:read('*l')=='PILOT_VERIFIED1\t'..(pilot_plan_context or '') then
                for i=1,512 do
                    local line=f:read('*l');if not line then break end;if #line>512 then break end
                    local r=fields(line);local p,expires=tonumber(r[4]),tonumber(r[6])
                    if #r==6 and host_ok(r[1]) and scope_ok(r[2],r[3],p) and id_ok(r[5]) and expires and expires>now and expires<=now+8*24*60*60 then
                        next_routes[route_key(r[1],r[2],r[3],p)]={id=r[5],expires=expires}
                    end
                end
            end
            f:close()
        end
        routes=next_routes
    end
    local r=routes[route_key(host,tls,family,port)]
    return r and r.expires>now and r.id or nil
end

function pilot_trial(ctx,d)
    orchestrate(ctx,d)
    if d.fwmark~=0x200000dc then return VERDICT_PASS end
    if (d.outgoing and d.ifin and #d.ifin>0) or (not d.outgoing and d.ifout and #d.ifout>0) then return VERDICT_PASS end
    if not d.track or not host_ok(d.track.hostname) or not d.dis or not d.dis.tcp then return VERDICT_PASS end
    local family=d.dis.ip6 and 'ipv6' or 'ipv4'
    local port=tonumber(d.outgoing and d.dis.tcp.th_dport or d.dis.tcp.th_sport)
    d.track.lua_state=d.track.lua_state or {};local state=d.track.lua_state
    if not state.pilot_trial then
        local f=pilot_runtime and io.open(pilot_runtime..'/probe-command.tsv','r');if not f then return VERDICT_PASS end
        local line=f:read('*l');f:close();if not line or #line>640 then return VERDICT_PASS end
        local r=fields(line);local expires=tonumber(r[8]);local now=os.time()
        local variant=pilot_tls_variant and pilot_tls_variant(d.reasm_data or d.dis.payload)
        if #r~=8 or r[1]~='PILOT_TRIAL1' or #r[2]~=32 or not r[2]:match('^[a-f0-9]+$') or not host_ok(r[3]) or not id_ok(r[4]) or not scope_ok(r[5],r[6],tonumber(r[7])) or not expires or expires<now or expires>now+120 or r[3]~=d.track.hostname or r[5]~=variant or r[6]~=family or tonumber(r[7])~=port then return VERDICT_PASS end
        state.pilot_trial={nonce=r[2],host=r[3],id=r[4],tls=r[5],family=family,port=port,expires=expires}
    end
    local trial=state.pilot_trial
    -- A connection retains its trial even when the next candidate is published.
    local verdict,count=VERDICT_PASS,0
    while true do
        local instance=plan_instance_pop(d);if not instance then break end
        if instance.arg.pilot_id==trial.id then verdict=plan_instance_execute(d,verdict,instance);count=count+1 end
    end
    if count>0 and d.outgoing and not state.pilot_proved and d.dis.payload and #d.dis.payload>0 and pilot_tls_variant(d.reasm_data or d.dis.payload)==trial.tls then
        local f=io.open(pilot_runtime..'/probe-proof.tsv','w')
        if f then
            f:write('PILOT_PROOF1\t',trial.nonce,'\t',trial.host,'\t',trial.id,'\t',trial.tls,'\t',trial.family,'\t',trial.port,'\nEND\t',trial.nonce,'\n');f:close();state.pilot_proved=true
        end
    end
    return verdict
end

-- A bounded private ring contains only ClientHello records from connections
-- that actually failed. It is not a capture of HTTP requests or credentials.
local hello_slots,hello_keys,hello_next={}, {},0
local hex={};for i=0,255 do hex[i]=string.format('%02x',i) end
function pilot_capture_hello(d,h)
    local state=d.track and d.track.lua_state
    if not state or not state.pilot_hello or not state.pilot_peer or not pilot_runtime then return end
    local host=d.track.hostname;local tls=pilot_tls_variant(state.pilot_hello)
    local family=d.dis.ip6 and 'ipv6' or 'ipv4';local port=h.pilot_port
    if not host_ok(host) or not scope_ok(tls,family,port) then return end
    local key=route_key(host,tls,family,port);local now=os.time();local slot=hello_slots[key]
    if slot and slot.at and now>=slot.at and now-slot.at<60 then return end
    if not slot then
        local index=hello_next;hello_next=(hello_next+1)%64
        if hello_keys[index] then hello_slots[hello_keys[index]]=nil end
        hello_keys[index]=key;slot={index=index};hello_slots[key]=slot
    end
    local f=io.open(pilot_runtime..'/hello'..string.format('%02d',slot.index)..'.tsv','w')
    if f then
        local encoded=state.pilot_hello:gsub('.',function(c)return hex[c:byte()] end)
        f:write('PILOT_HELLO1\t',host,'\t',tls,'\t',family,'\t',port,'\t',state.pilot_peer,'\t',now,'\t',encoded,'\nEND\n');f:close();slot.at=now
    end
end
