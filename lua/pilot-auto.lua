-- Keenetic Pilot: per-host persistence and observations around nfqws2's
-- documented circular/failure-detector API. No external scripts are loaded.
local cache, generation, dirty, last_write = {}, 0, false, 0
local maximum = 512
local discovered, discovered_count = {}, 0
local queues = {}
local function valid_host(h)
    if type(h)~='string' or #h>253 or not h:match('^[a-z0-9][a-z0-9%.%-]*%.[a-z][a-z0-9%-]*$') or h:find('%.%.') then return false end
    for label in h:gmatch('[^.]+') do if #label>63 or label:sub(1,1)=='-' or label:sub(-1)=='-' then return false end end
    return true
end
local function valid_id(s) return type(s)=='string' and #s<=96 and s:match('^[a-z0-9_-]+$') end
-- Read only the ClientHello framing needed to distinguish TLS 1.2 from 1.3.
-- Incomplete/ECH-hidden data remains in an explicit unknown bucket.
local function tls_variant(s)
    if type(s)~='string' or #s<44 or s:byte(1)~=22 or s:byte(6)~=1 then return nil end
    local function u16(p) if p+1>#s then return nil end;return s:byte(p)*256+s:byte(p+1) end
    local record=u16(4);if not record or record+5>#s then return nil end
    local p=45+s:byte(44)
    local n=u16(p);if not n then return nil end;p=p+2+n
    if p>#s then return nil end;p=p+1+s:byte(p)
    local total=u16(p);if not total then return nil end;p=p+2
    local last=p+total-1;if last>#s then return nil end
    while p+3<=last do
        local kind=u16(p);local length=u16(p+2);p=p+4
        if p+length-1>last then return nil end
        if kind==43 and length>=3 then
            local count=s:byte(p)
            if count%2~=0 or count+1>length then return nil end
            for j=p+1,p+count,2 do if u16(j)==772 then return 'tls13' end end
        end
        p=p+length
    end
    if p~=last+1 then return nil end
    return s:byte(10)==3 and s:byte(11)==3 and 'tls12' or 'legacy'
end
local function read_slot(path)
    local f=io.open(path,'r'); if not f then return nil end
    local header=f:read('*l'); local n=header and tonumber(header:match('^PILOT1\t(%d+)$'))
    local records={};local count=0;local complete=false
    if n then
        for line in f:lines() do
            if line=='END\t'..n then complete=true;break end
            local group,host,id,status,stamp=line:match('^([^\t]+)\t([^\t]+)\t([^\t]+)\t([^\t]+)\t(%d+)$')
            if not (valid_id(group) and valid_host(host) and valid_id(id) and (status=='probing' or status=='connected')) then break end
            count=count+1;if count>maximum then break end
            records[group..'\t'..host]={group=group,host=host,id=id,status=status,stamp=tonumber(stamp)}
        end
    end
    f:close();if complete then return n,records end
end
local function load()
    if not pilot_runtime then return end
    local list=io.open(pilot_runtime..'/autohosts.txt','r')
    if list then for host in list:lines() do if valid_host(host) and not discovered[host] and discovered_count<1000 then discovered[host]=true;discovered_count=discovered_count+1 end end;list:close() end
    for i=0,1 do
        local n,records=read_slot(pilot_runtime..'/state'..i..'.tsv')
        if n and n>generation then generation=n;cache=records end
    end
end
local function flush()
    local now=os.time();if not dirty or now-last_write<2 or not pilot_runtime then return end
    local n=generation+1;local f=io.open(pilot_runtime..'/state'..(n%2)..'.tsv','w')
    if not f then return end
    f:write('PILOT1\t',n,'\n')
    for _,r in pairs(cache) do f:write(r.group,'\t',r.host,'\t',r.id,'\t',r.status,'\t',r.stamp,'\n') end
    f:write('END\t',n,'\n');f:close();generation=n;last_write=now;dirty=false
end
local function remember(group,host,id,status,newsite)
    if newsite and id=='none' then return end
    if newsite and not discovered[host] and discovered_count<1000 and pilot_runtime then
        local f=io.open(pilot_runtime..'/autohosts.txt','a')
        if f then f:write(host,'\n');f:close();discovered[host]=true;discovered_count=discovered_count+1 end
    end
    local key=group..'\t'..host;local old=cache[key]
    if old and old.id==id and old.status==status then return end
    local count=0;local oldest_key,oldest_stamp
    for k,r in pairs(cache) do count=count+1;if not oldest_stamp or r.stamp<oldest_stamp then oldest_key=k;oldest_stamp=r.stamp end end
    if not old and count>=maximum then cache[oldest_key]=nil end
    cache[key]={group=group,host=host,id=id,status=status,stamp=os.time()};dirty=true
    print('PILOT adaptive '..host..' '..id..' '..status)
    flush()
end
function pilot_failure(d,c)
    -- Early fatal TLS alerts are visible for TLS 1.2 before encryption.
    -- A failed connection is a heuristic, not proof of DPI intervention.
    local payload=d.dis and d.dis.payload
    if not d.outgoing and d.dis.tcp and payload and #payload>=7 and payload:byte(1)==21 and payload:byte(6)==2 then return true end
    return standard_failure_detector(d,c)
end
function pilot_success(d,c)
    local ok=standard_success_detector(d,c)
    if ok then local h=automate_host_record(d);if h then h.pilot_connected=true end end
    return ok
end
function pilot_pass(ctx,d) return VERDICT_PASS end
function pilot_hostkey(d) return d.track and d.track.hostname end
function pilot_auto(ctx,d)
    flush()
    -- Always take over the plan, including packets without a visible SNI.
    -- Otherwise all tagged candidate actions would run consecutively.
    orchestrate(ctx,d)
    if not d.track then return VERDICT_PASS end
    local host=d.track.hostname
    if not valid_host(host) then return VERDICT_PASS end
    local group=d.arg.key or 'unknown';local newsite=group=='discovered'
    d.track.lua_state=d.track.lua_state or {}
    local variant=d.track.lua_state.pilot_tls_variant
    if not variant then variant=tls_variant(d.reasm_data or (d.dis and d.dis.payload));if variant then d.track.lua_state.pilot_tls_variant=variant end end
    group=group..'_'..(variant or 'unknown')..(d.dis.ip6 and '_ipv6' or '_ipv4');if not valid_id(group) then return VERDICT_PASS end
    d.arg.key=group
    d.arg.hostkey='pilot_hostkey'
    local ids,indices={},{}
    for _,instance in pairs(d.plan or {}) do
        local n=tonumber(instance.arg.strategy)
        if n and instance.arg.pilot_id then ids[n]=instance.arg.pilot_id;indices[instance.arg.pilot_id]=n end
    end
    local h=automate_host_record(d)
    if not h then return VERDICT_PASS end
    if not h.pilot_loaded then
        -- Bound transient host records as well as the persisted observations.
        local q=queues[group] or {first=1,last=0};queues[group]=q
        q.last=q.last+1;q[q.last]=host
        if q.last-q.first>=1024 then
            local evicted=q[q.first];q[q.first]=nil;q.first=q.first+1
            if evicted~=host and autostate and autostate[group] then autostate[group][evicted]=nil end
        end
        local old=cache[group..'\t'..host]
        if old and indices[old.id] then h.nstrategy=indices[old.id] end
        h.pilot_loaded=true
    end
    local before=h.nstrategy or 1;h.pilot_connected=nil
    local verdict=circular(ctx,d)
    local current=h.nstrategy or 1
    if current~=before and ids[current] then remember(group,host,ids[current],'probing',newsite)
    elseif h.pilot_connected and ids[current] then remember(group,host,ids[current],'connected',newsite) end
    return verdict
end
load()
