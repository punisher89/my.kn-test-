-- Keenetic Pilot: per-host persistence and observations around nfqws2's
-- documented circular/failure-detector API. No external scripts are loaded.
local cache, generation, dirty, last_write = {}, 0, false, 0
local maximum = 512
local discovered, discovered_count = {}, 0
local queues = {}
local history = {}
local history_limit = 128
local learned_lifetime = 7*24*60*60
local reasons = {unknown=true, rst=true, retransmission=true, tls_alert=true}
local origins = {initial=true, pool=true, learned=true, restored=true}
local function network() return pilot_network and #pilot_network>0 and pilot_network or '-' end
local function valid_network(s) return type(s)=='string' and #s>0 and #s<=15 and s:match('^[a-zA-Z0-9_.:%-]+$') end
local function context(group)
    return group:match('_(tls12)_ipv[46]$') or group:match('_(tls13)_ipv[46]$'), group:match('_(ipv[46])$')
end
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
    local header=f:read('*l'); local version,n
    if header then version,n=header:match('^PILOT([12])\t(%d+)$');n=tonumber(n) end
    local records,events={},{};local count=0;local complete=false
    if n then
        for line in f:lines() do
            if line=='END\t'..n then complete=true;break end
            local fields={};for v in (line..'\t'):gmatch('(.-)\t') do fields[#fields+1]=v end
            local kind,group,host,id,status,stamp,reason,net,port,origin
            if version=='1' and #fields==5 then
                kind='R';group,host,id,status,stamp=table.unpack(fields)
                reason,net,port,origin='unknown','-',0,'restored'
            elseif version=='2' and #fields==10 then
                kind,group,host,id,status,stamp,reason,net,port,origin=table.unpack(fields);port=tonumber(port)
            end
            stamp=tonumber(stamp)
            local valid_status=(kind=='R' and (status=='probing' or status=='connected')) or (kind=='E' and (status=='failed' or status=='selected' or status=='connected'))
            if not (valid_id(group) and valid_host(host) and valid_id(id) and valid_status and stamp and stamp>=0 and reasons[reason] and valid_network(net) and port and port>=0 and port<=65535 and port%1==0 and origins[origin]) then break end
            local row={group=group,host=host,id=id,status=status,stamp=stamp,reason=reason,network=net,port=port,origin=origin}
            if kind=='R' then
                count=count+1;if count>maximum then break end
                records[group..'\t'..host]=row
            else
                events[#events+1]=row;if #events>history_limit then break end
            end
        end
    end
    f:close();if complete then return n,records,events end
end
local function load()
    if not pilot_runtime then return end
    local list=io.open(pilot_runtime..'/autohosts.txt','r')
    if list then for host in list:lines() do if valid_host(host) and not discovered[host] and discovered_count<1000 then discovered[host]=true;discovered_count=discovered_count+1 end end;list:close() end
    for i=0,1 do
        local n,records,events=read_slot(pilot_runtime..'/state'..i..'.tsv')
        if n and n>generation then generation=n;cache=records;history=events end
    end
end
local function flush()
    local now=os.time();if not dirty or now-last_write<2 or not pilot_runtime then return end
    local n=generation+1;local f=io.open(pilot_runtime..'/state'..(n%2)..'.tsv','w')
    if not f then return end
    f:write('PILOT2\t',n,'\n')
    local function row(kind,r)
        f:write(kind,'\t',r.group,'\t',r.host,'\t',r.id,'\t',r.status,'\t',r.stamp,'\t',r.reason,'\t',r.network,'\t',r.port,'\t',r.origin,'\n')
    end
    for _,r in pairs(cache) do row('R',r) end
    for _,r in ipairs(history) do row('E',r) end
    f:write('END\t',n,'\n');f:close();generation=n;last_write=now;dirty=false
end
local function remember(group,host,id,status,newsite,h)
    if newsite and id=='none' then return end
    if newsite and not discovered[host] and discovered_count<1000 and pilot_runtime then
        local f=io.open(pilot_runtime..'/autohosts.txt','a')
        if f then f:write(host,'\n');f:close();discovered[host]=true;discovered_count=discovered_count+1 end
    end
    if h.pilot_legacy then cache[h.pilot_legacy]=nil;h.pilot_legacy=nil end
    local key=group..'\t'..host;local old=cache[key]
    if old and old.id==id and old.status==status and old.reason==h.pilot_reason and old.network==network() and old.port==h.pilot_port and os.time()-old.stamp<3600 then return end
    local count=0;local oldest_key,oldest_stamp
    for k,r in pairs(cache) do count=count+1;if not oldest_stamp or r.stamp<oldest_stamp then oldest_key=k;oldest_stamp=r.stamp end end
    if not old and count>=maximum then cache[oldest_key]=nil end
    cache[key]={group=group,host=host,id=id,status=status,stamp=os.time(),reason=h.pilot_reason or 'unknown',network=network(),port=h.pilot_port or 0,origin=h.pilot_origin or 'initial'};dirty=true
    print('PILOT adaptive '..host..' '..id..' '..status)
    flush()
end
local function event(group,host,id,status,h,origin)
    if not id then return end
    history[#history+1]={group=group,host=host,id=id,status=status,stamp=os.time(),reason=h.pilot_reason or 'unknown',network=network(),port=h.pilot_port or 0,origin=origin or h.pilot_origin or 'initial'}
    if #history>history_limit then table.remove(history,1) end
    dirty=true
end
-- Similar observations propose candidates; each new destination still has to
-- succeed with its own traffic. Unknown, stale and other-route records cannot donate.
local function learned_candidates(group,host,h,indices)
    local tls,ip=context(group)
    if pilot_reuse==false or not tls or not ip or h.pilot_reason=='unknown' or network()=='-' or h.pilot_port==0 then return {} end
    local scores={};local now=os.time();local latest={}
    for _,r in pairs(cache) do
        local rtls,rip=context(r.group)
        if r.host~=host and r.network==network() and r.port==h.pilot_port and rtls==tls and rip==ip then
            local old=latest[r.host]
            if not old or r.stamp>old.stamp or (r.stamp==old.stamp and ((r.status=='probing' and old.status~='probing') or (r.status==old.status and r.group<old.group))) then latest[r.host]=r end
        end
    end
    for _,r in pairs(latest) do
        local rtls,rip=context(r.group)
        if r.host~=host and r.status=='connected' and r.id~='none' and indices[r.id] and r.reason==h.pilot_reason and r.network==network() and r.port==h.pilot_port and rtls==tls and rip==ip and now>=r.stamp and now-r.stamp<=learned_lifetime then
            local score=scores[r.id] or {id=r.id,count=0,latest=0,hosts={}};scores[r.id]=score
            if not score.hosts[r.host] then score.count=score.count+1;score.hosts[r.host]=true end;score.latest=math.max(score.latest,r.stamp)
        end
    end
    local result={};for _,score in pairs(scores) do result[#result+1]=score end
    table.sort(result,function(a,b) if a.count~=b.count then return a.count>b.count end;if a.latest~=b.latest then return a.latest>b.latest end;return a.id<b.id end)
    while #result>3 do table.remove(result) end
    return result
end
local function rotation(h,group,host,ids,indices)
    -- Upstream circular has already advanced its index. Override only the next
    -- connection's choice; the native per-connection generation remains intact.
    if not h.pilot_order then
        local order={{n=h.pilot_before,origin=h.pilot_origin or 'initial'}};local used={[h.pilot_before]=true}
        for _,candidate in ipairs(learned_candidates(group,host,h,indices)) do
            local n=indices[candidate.id]
            if not used[n] then order[#order+1]={n=n,origin='learned'};used[n]=true end
        end
        for offset=1,#ids-1 do
            local n=(h.pilot_before-1+offset)%#ids+1
            if not used[n] then order[#order+1]={n=n,origin='pool'} end
        end
        h.pilot_order=order;h.pilot_cursor=1
    end
    h.pilot_cursor=h.pilot_cursor%#h.pilot_order+1
    local next=h.pilot_order[h.pilot_cursor]
    h.nstrategy=next.n;h.pilot_origin=next.origin
    circular_sync_generation(h)
    if h.pilot_cursor==1 then h.pilot_order=nil end
end
function pilot_failure(d,c)
    -- These are symptoms, not proof of a provider's physical DPI appliance.
    local payload=d.dis and d.dis.payload
    local failed,outcome,reason
    if not d.outgoing and d.dis.tcp and payload and #payload>=7 and payload:byte(1)==21 and payload:byte(6)==2 then
        failed=true;reason='tls_alert'
    else
        failed,outcome=standard_failure_detector(d,c)
        if failed then reason=d.outgoing and 'retransmission' or 'rst' end
    end
    if failed then
        local h=automate_host_record(d)
        h.pilot_reason=reason
        d.pilot_failed_strategy=d.pilot_ids and d.pilot_ids[c.nstrategy]
    end
    return failed,outcome
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
    local suffix='_'..(variant or 'unknown')..(d.dis.ip6 and '_ipv6' or '_ipv4')
    local port=tonumber(d.outgoing and d.dis.tcp.th_dport or d.dis.tcp.th_sport) or 0
    local legacy_group=group..suffix
    group=group..'_p'..port..suffix;if not valid_id(group) then return VERDICT_PASS end
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
        if not old then
            h.pilot_legacy=legacy_group..'\t'..host
            old=cache[h.pilot_legacy]
        end
        h.pilot_port=port
        if old and indices[old.id] then
            h.nstrategy=indices[old.id];h.pilot_origin=old.origin
            if old.network==network() and old.port==h.pilot_port then h.pilot_reason=old.reason end
        end
        h.pilot_reason=h.pilot_reason or 'unknown';h.pilot_origin=h.pilot_origin or 'initial'
        h.on_strategy_changed=function(record) rotation(record,group,host,ids,indices) end
        h.pilot_loaded=true
    end
    local before=h.nstrategy or 1;local before_origin=h.pilot_origin;h.pilot_before=before;h.pilot_connected=nil
    d.pilot_ids=ids
    local verdict=circular(ctx,d)
    local current=h.nstrategy or 1
    if d.pilot_failed_strategy then
        -- Attribute the failure to the old method, before recording its replacement.
        event(group,host,d.pilot_failed_strategy,'failed',h,before_origin)
    end
    if current~=before and ids[current] then
        event(group,host,ids[current],'selected',h)
        remember(group,host,ids[current],'probing',newsite,h)
    elseif h.pilot_connected and ids[current] then
        local old=cache[group..'\t'..host]
        if not old or old.id~=ids[current] or old.status~='connected' then
            if not (newsite and ids[current]=='none') then event(group,host,ids[current],'connected',h) end
        end
        remember(group,host,ids[current],'connected',newsite,h)
    elseif d.pilot_failed_strategy and ids[current] then
        remember(group,host,ids[current],'probing',newsite,h)
    end
    flush()
    return verdict
end
load()
