package pilot

import (
 "os"
 "path/filepath"
 "strconv"
 "strings"
 "sync"
 "time"
)

// A bounded, read-only CPU sampler. Its percentages are fractions of the
// *whole router*, not fractions of a single core. The first sample is unknown.
// It reads only /proc/stat and the two Pilot-owned PIDs, not every process.
type pilotProcessCPU struct {
 mu sync.Mutex
 at time.Time
 root string
 ownPID,enginePID int
 total,own,engine uint64
 valid,ownValid,engineValid bool
 lastOwn,lastEngine *float64
}

func cpuTicks(raw []byte) (uint64,bool) {
 lines:=strings.SplitN(string(raw),"\n",2)
 fields:=strings.Fields(lines[0])
 if len(fields)<5||fields[0]!="cpu" {return 0,false}
 var total uint64
 for _,v:=range fields[1:] {
    n,e:=strconv.ParseUint(v,10,64)
    if e!=nil {return 0,false}
    total+=n
 }
 return total,true
}
func processTicks(raw []byte) (uint64,bool) {
 // comm in /proc/PID/stat can contain spaces or ')'; take the last ')'.
 right:=strings.LastIndexByte(string(raw),')')
 if right<0{return 0,false}
 fields:=strings.Fields(string(raw[right+1:]))
 // Field 3 (state) is fields[0], fields 14+15 are fields[11:13].
 if len(fields)<13{return 0,false}
 u,e1:=strconv.ParseUint(fields[11],10,64)
 s,e2:=strconv.ParseUint(fields[12],10,64)
 if e1!=nil||e2!=nil{return 0,false}
 return u+s,true
}
func readCPUTicks(root string,pid int)(uint64,bool){
 if pid<=0{return 0,false}
 b,err:=os.ReadFile(filepath.Join(root,"proc",strconv.Itoa(pid),"stat"))
 if err!=nil||len(b)>8192{return 0,false}
 return processTicks(b)
}
func cpuShare(old,new,oldTotal,newTotal uint64)(*float64){
 if newTotal<=oldTotal||new<old{return nil}
 value:=float64(new-old)*100/float64(newTotal-oldTotal)
 if value<0 || value>100 {return nil}
 return &value
}
func (p *pilotProcessCPU) sample(root string,ownPID,enginePID int,now time.Time)(*float64,*float64){
 p.mu.Lock()
 defer p.mu.Unlock()
 // Avoid repeated procfs reads during overlapping device/diagnostic requests.
 if !p.at.IsZero()&&p.root==root&&p.ownPID==ownPID&&p.enginePID==enginePID&&
    !now.Before(p.at)&&now.Sub(p.at)<3*time.Second {
    return p.lastOwn,p.lastEngine
 }
 host,err:=os.ReadFile(filepath.Join(root,"proc/stat"))
 if err!=nil||len(host)>16384 {p.valid=false;p.lastOwn=nil;p.lastEngine=nil;return nil,nil}
 total,ok:=cpuTicks(host)
 if !ok {p.valid=false;p.lastOwn=nil;p.lastEngine=nil;return nil,nil}
 own,ownOK:=readCPUTicks(root,ownPID)
 engine,engineOK:=readCPUTicks(root,enginePID)
 var ownShare,engineShare *float64
 if p.valid&&p.root==root&&p.total<total {
    if ownOK&&p.ownValid&&p.ownPID==ownPID {ownShare=cpuShare(p.own,own,p.total,total)}
    if engineOK&&p.engineValid&&p.enginePID==enginePID {engineShare=cpuShare(p.engine,engine,p.total,total)}
 }
 p.root,p.at= root,now
 p.ownPID,p.enginePID=ownPID,enginePID
 p.total,p.own,p.engine=total,own,engine
 p.valid,p.ownValid,p.engineValid=true,ownOK,engineOK
 p.lastOwn,p.lastEngine=ownShare,engineShare
 return ownShare,engineShare
}
