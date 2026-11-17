package pilot

import (
 "context"
 "errors"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strconv"
 "strings"
 "sync"
 "syscall"
 "time"
)

// OpenWrtRouter uses an isolated nftables table instead of Keenetic's
// iptables/PPE/NDM hooks. NEVER flush or modify inet fw4.
type OpenWrtRouter struct {
 mu sync.Mutex
 cmd *exec.Cmd
 done chan error
 installed bool
 log *LogBuffer
}
const openwrtNftTable = "keenetic_pilot"
const openwrtRuleTag = "keenetic-pilot:openwrt"

func NewOpenWrtRouter(log *LogBuffer) *OpenWrtRouter {
 return &OpenWrtRouter{log:log}
}
func (r *OpenWrtRouter) Kind() string { return "openwrt" }

func IsOpenWrt() bool {
 if _,err:=os.Stat("/etc/openwrt_release");err!=nil{return false}
 _,err:=os.Stat("/etc/rc.common")
 return err==nil
}

// Preview is a pure rendering function, and never changes network rules.
// The bounded per-direction window avoids queueing complete connections.
func OpenWrtNftRules(c Config) (string,error) {
 if err:=c.Validate();err!=nil {return "",err}
 if c.WAN=="" || !interfaceRE.MatchString(c.WAN){return "",errors.New("OpenWrt: укажите WAN-интерфейс")}
 if c.PPE {return "",errors.New("OpenWrt: Keenetic PPE не поддерживается; отключите ppe")}
 if c.Adaptive && c.AdaptiveActive {return "",errors.New("OpenWrt: активный подбор с CONNMARK пока не поддерживается")}
 // Never permit a table name, interface, port, or queue value from arbitrary text.
 family:="meta nfproto ipv4 "
 if c.IPv6 {family=""}
 tcpPorts:=[]string{"80","443","2053","2083","2087","2096","8443"}
 if c.TelegramMTProto {tcpPorts=append(tcpPorts,"5222")}
 udpPorts:=[]string{}
 if c.QUIC {udpPorts=append(udpPorts,"443")}
 if c.DiscordVoice {udpPorts=append(udpPorts,"50000-50099")}
 // ct packets measures per direction; 20/30 are initial handshake windows,
 // not an entire traffic capture. Smaller windows favor 2-core routers.
 maxPackets:=20
 if c.Adaptive {maxPackets=30}
 out:=&strings.Builder{}
 fmt.Fprintf(out,"add table inet %s\n",openwrtNftTable)
 fmt.Fprintf(out,"add chain inet %s pilot_pre { type filter hook prerouting priority -149; policy accept; }\n",openwrtNftTable)
 fmt.Fprintf(out,"add chain inet %s pilot_post { type filter hook postrouting priority -149; policy accept; }\n",openwrtNftTable)
 common:=func(chain, direction,iface,ctdir string,proto,ports string){
   fmt.Fprintf(out,"add rule inet %s %s %s%s %q meta mark & 0x40000000 == 0 ct direction %s ct %s packets <= %d %s dport { %s } queue num %d bypass comment %q\n",
      openwrtNftTable,chain,family,direction,iface,ctdir,ctdir,maxPackets,proto,ports,c.Queue,openwrtRuleTag)
 }
 common("pilot_post","oifname",c.WAN,"original","tcp",strings.Join(tcpPorts,", "))
 common("pilot_pre","iifname",c.WAN,"reply","tcp",strings.Join(tcpPorts,", "))
 if len(udpPorts)>0 {
    ports:=strings.Join(udpPorts,", ")
    common("pilot_post","oifname",c.WAN,"original","udp",ports)
    common("pilot_pre","iifname",c.WAN,"reply","udp",ports)
 }
 return out.String(),nil
}

func nftCommand(input string, args ...string) (string,error) {
 ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second)
 defer cancel()
 cmd:=exec.CommandContext(ctx,"nft",args...)
 if input!="" {cmd.Stdin=strings.NewReader(input)}
 out,err:=cmd.CombinedOutput()
 if err!=nil {return "",fmt.Errorf("nft %s: %w: %s",strings.Join(args," "),err,tail(out,1600))}
 return string(out),nil
}

func nftOwnTable() (bool,error) {
 out,err:=nftCommand("","list","table","inet",openwrtNftTable)
 if err!=nil {
    // On a real OpenWrt device, ENOENT from list must not be mistaken for
    // failure to execute nft or lack of netlink permissions.
    if strings.Contains(err.Error(),"No such file or directory") && strings.Contains(err.Error(),"nft list table") {
       return false,nil
    }
    return false,err
 }
 return strings.Contains(out,openwrtRuleTag),nil
}
func (r *OpenWrtRouter) Running() bool {
 r.mu.Lock();defer r.mu.Unlock()
 return r.running()
}
func (r *OpenWrtRouter) running() bool {
 if r.cmd==nil{return false}
 select {
 case err:=<-r.done:
   r.cmd=nil
   if err!=nil {r.log.Add("OpenWrt nfqws2 завершился: "+err.Error())}
   return false
 default:return true
 }
}
func (r *OpenWrtRouter) Repair() error {
 r.mu.Lock();defer r.mu.Unlock()
 if !r.running(){return errors.New("OpenWrt nfqws2 не запущен")}
 // Firewall reload does not normally touch an independently owned table;
 // do not replace the whole firewall or modify existing table contents.
 exists,err:=nftCommand("","list","table","inet",openwrtNftTable)
 if err!=nil{return fmt.Errorf("OpenWrt: правила NFQUEUE недоступны: %w",err)}
 if !strings.Contains(exists,openwrtRuleTag){return errors.New("OpenWrt: таблица NFQUEUE не принадлежит Pilot")}
 return nil
}
func (r *OpenWrtRouter) Start(c Config) error {
 r.mu.Lock();defer r.mu.Unlock()
 if os.Geteuid()!=0{return errors.New("OpenWrt: nfqueue требует root")}
 if !IsOpenWrt(){return errors.New("OpenWrt: /etc/openwrt_release или procd не обнаружены; backend не включён")}
 if _,err:=exec.LookPath("nft");err!=nil{return errors.New("OpenWrt: нужна программа nft и kmod-nft-queue")}
 if _,err:=exec.LookPath("fw4");err!=nil{return errors.New("OpenWrt: firewall4 (fw4) не найден")}
 if _,err:=os.Stat(filepath.Join("/sys/class/net",c.WAN));err!=nil {return fmt.Errorf("OpenWrt WAN %q не найден: %w",c.WAN,err)}
 rules,err:=OpenWrtNftRules(c)
 if err!=nil{return err}
 if err=CheckEngine(c);err!=nil{return err}
 if err=r.stop();err!=nil{return err}
 // Check syntax and netlink prerequisites without changing firewall.
 if _,err=nftCommand(rules,"-c","-f","-");err!=nil{return err}
 // Never modify a pre-existing table of the same name (even after crash).
 if _,err=nftCommand("","list","table","inet",openwrtNftTable);err==nil {
   return errors.New("OpenWrt: таблица keenetic_pilot уже существует — сначала проверьте, не запущен ли другой экземпляр; правила не изменены")
 }
 r.cmd=exec.Command(filepath.Join(c.EngineDir,"nfqws2"),EngineArgs(c)...)
 r.cmd.Stdout=r.log;r.cmd.Stderr=r.log
 r.cmd.SysProcAttr=&syscall.SysProcAttr{Pdeathsig:syscall.SIGTERM}
 if err=r.cmd.Start();err!=nil{r.cmd=nil;return err}
 cmd:=r.cmd
 r.done=make(chan error,1)
 go func(){r.done<-cmd.Wait();close(r.done)}()
 select {
 case err=<-r.done:
    r.cmd=nil
    return fmt.Errorf("OpenWrt nfqws2 не запустился: %w",err)
 case <-time.After(350*time.Millisecond):
 }
 if _,err=nftCommand(rules,"-f","-");err!=nil {
   return errors.Join(err,r.stop())
 }
 r.installed=true
 r.log.Add("OpenWrt: собственная nft-таблица NFQUEUE включена; WAN="+c.WAN+"; очередь="+strconv.Itoa(c.Queue))
 return nil
}
func (r *OpenWrtRouter) Stop() error {r.mu.Lock();defer r.mu.Unlock();return r.stop()}
func (r *OpenWrtRouter) stop() error {
 var errs error
 // Remove our own nft table before stopping nfqws2; no queued traffic is left
 // waiting for a missing user-space listener.
 if r.installed {
   exists,err:=nftCommand("","list","table","inet",openwrtNftTable)
   if err!=nil {errs=errors.Join(errs,err)
   }else if !strings.Contains(exists,openwrtRuleTag) {
     errs=errors.Join(errs,errors.New("OpenWrt: найдено чужое содержимое таблицы, удаление запрещено"))
   }else if _,err=nftCommand("","delete","table","inet",openwrtNftTable);err!=nil{
     errs=errors.Join(errs,err)
   }else{r.installed=false}
 }
 if r.cmd!=nil {
    _=r.cmd.Process.Signal(syscall.SIGTERM)
    select {
    case <-r.done:
    case <-time.After(3*time.Second):
      _=r.cmd.Process.Kill()
      <-r.done
    }
    r.cmd=nil
 }
 return errs
}
