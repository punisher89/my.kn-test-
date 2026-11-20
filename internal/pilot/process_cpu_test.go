package pilot

import (
 "fmt"
 "math"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

func writeProcessFixture(t *testing.T,root string,pid int,utime,stime uint64) {
 t.Helper()
 fields:=make([]string,13)
 fields[0]="S"
 for i:=1;i<len(fields);i++ {fields[i]="0"}
 fields[11]=fmt.Sprint(utime);fields[12]=fmt.Sprint(stime)
 path:=filepath.Join(root,"proc",fmt.Sprint(pid),"stat")
 if err:=os.MkdirAll(filepath.Dir(path),0700);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(path,[]byte(fmt.Sprintf("%d (pilot daemon) %s\n",pid,strings.Join(fields," "))),0600);err!=nil{t.Fatal(err)}
}
func writeHostCPU(t *testing.T,root string,user,idle int) {
 t.Helper()
 if err:=os.MkdirAll(filepath.Join(root,"proc"),0700);err!=nil{t.Fatal(err)}
 value:=fmt.Sprintf("cpu %d 0 0 %d 0 0 0 0 0 0\ncpu0 0 0 0 0\n",user,idle)
 if err:=os.WriteFile(filepath.Join(root,"proc/stat"),[]byte(value),0600);err!=nil{t.Fatal(err)}
}
func TestProcessCPUReportsOnlyMeasuredContributions(t *testing.T){
 root:=t.TempDir()
 writeHostCPU(t,root,1000,1000)
 writeProcessFixture(t,root,100,200,50)
 writeProcessFixture(t,root,200,300,10)
 p:=pilotProcessCPU{}
 now:=time.Unix(1000,0)
 a,b:=p.sample(root,100,200,now)
 if a!=nil||b!=nil {t.Fatal("first sample invented CPU percentage")}
 writeHostCPU(t,root,1100,1100)
 writeProcessFixture(t,root,100,215,55)
 writeProcessFixture(t,root,200,340,10)
 a,b=p.sample(root,100,200,now.Add(4*time.Second))
 if a==nil||math.Abs(*a-10)>0.001||b==nil||math.Abs(*b-20)>0.001 {
   t.Fatalf("incorrect process shares: Pilot=%v nfqws2=%v",a,b)
 }
 // Repeated requests should reuse the same sample without procfs reads.
 writeHostCPU(t,root,1300,1100)
 a,b=p.sample(root,100,200,now.Add(4*time.Second+time.Millisecond))
 if a==nil||math.Abs(*a-10)>0.001||b==nil||math.Abs(*b-20)>0.001 {
   t.Fatal("resource polling bypassed 3s cache",a,b)
 }
 // Daemon restart must not attribute the previous process's CPU to its successor.
 writeProcessFixture(t,root,201,2,2)
 a,b=p.sample(root,100,201,now.Add(9*time.Second))
 if b!=nil {t.Fatal("new PID reused old process CPU sample")}
 if a==nil {t.Fatal("valid Pilot sample lost when engine changed")}
}
func TestProcessCPUOmittedWhenKernelDataMissingOrMalformed(t *testing.T){
 root:=t.TempDir()
 writeHostCPU(t,root,100,100)
 writeProcessFixture(t,root,100,20,5)
 p:=pilotProcessCPU{}
 now:=time.Unix(1000,0)
 p.sample(root,100,0,now)
 os.WriteFile(filepath.Join(root,"proc/100/stat"),[]byte("100 (bad data) S no numbers\n"),0600)
 writeHostCPU(t,root,110,130)
 a,b:=p.sample(root,100,200,now.Add(4*time.Second))
 if a!=nil||b!=nil{t.Fatal("missing or broken process ticks fabricated CPU sample")}
 if _,ok:=processTicks([]byte("123 (a bad) task) S 0"));ok {t.Fatal("malformed stat accepted")}
 if got:=cpuShare(20,19,100,200);got!=nil {t.Fatal("negative process ticks accepted")}
 if got:=cpuShare(20,220,100,200);got!=nil {t.Fatal("impossible CPU contribution accepted")}
}
