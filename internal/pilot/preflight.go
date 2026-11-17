package pilot

import (
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"
)

// RouterPreflight is a bounded, read-only summary inspired by the separate
// MY.KN diagnostic project. It does not start processes or alter firewall.
type RouterPreflight struct {
    Checked bool `json:"checked"`
    SpaceFreeBytes *uint64 `json:"opt_free_bytes,omitempty"`
    StorageKind string `json:"storage_kind"`
    OtherEngines []string `json:"other_engines"`
    InstalledServices []string `json:"installed_services"`
    ProcessListAvailable bool `json:"process_list_available"`
    Notes []string `json:"notes"`
}

func knownEngineName(name string) bool {
    if name=="nfqws"||name=="nfqws2"||name=="d2k"||name=="zapret"||name=="zapret2" { return true }
    for _, base := range []string{"nfqws","nfqws2","d2k","zapret","zapret2"} {
        if strings.HasPrefix(name,base+"-") || strings.HasPrefix(name,base+"_"){return true}
    }
    return false
}

func passivePreflight(proc, opt string, ownPID int) RouterPreflight {
    report:=RouterPreflight{Checked:true,StorageKind:"unknown",OtherEngines:[]string{},InstalledServices:[]string{},Notes:[]string{}}
    if entries,err:=os.ReadDir(proc);err==nil {
        report.ProcessListAvailable=true
        names:=map[string]bool{}
        scanned:=0
        for _,entry:=range entries {
            if _,err:=strconv.Atoi(entry.Name());err!=nil {continue}
            scanned++
            if scanned>4096 {report.ProcessListAvailable=false;break}
            pid,_:=strconv.Atoi(entry.Name())
            if pid==ownPID {continue}
            raw,err:=os.ReadFile(filepath.Join(proc,entry.Name(),"comm"))
            if os.IsNotExist(err) {continue}
            if err!=nil || len(raw)>128 {report.ProcessListAvailable=false;continue}
            name:=strings.TrimSpace(string(raw))
            if knownEngineName(name){names[name]=true}
        }
        for _,name:=range []string{"nfqws","nfqws2","d2k","zapret","zapret2"} {
            if names[name] {report.OtherEngines=append(report.OtherEngines,name);delete(names,name)}
        }
        for name:=range names {report.OtherEngines=append(report.OtherEngines,name)}
    }
    var stat syscall.Statfs_t
    if err:=syscall.Statfs(opt,&stat);err==nil {
        free:=stat.Bavail*uint64(stat.Bsize)
        report.SpaceFreeBytes=&free
        if free<32<<20 {report.Notes=append(report.Notes,"На /opt менее 32 МиБ свободного места")}
    } else {report.Notes=append(report.Notes,"Свободное место /opt определить не удалось")}
    if b,err:=os.ReadFile(filepath.Join(proc,"mounts"));err==nil {
        for _,line:=range strings.Split(string(b),"\n") {
            f:=strings.Fields(line)
            if len(f)<4 || f[1]!="/opt" {continue}
            switch f[2] {
            case "ubifs":report.StorageKind="possibly_internal_nand"
            case "ext4","ext3","ext2","f2fs":report.StorageKind="block_device_verify"
            case "tmpfs","overlay":report.StorageKind="virtual_or_overlay"
            default:report.StorageKind="unverified"
            }
            if strings.Contains(","+f[3]+",",",ro,") {report.Notes=append(report.Notes,"/opt смонтирован только для чтения")}
            break
        }
    }
    for _,name:=range []string{"S99d2k","S99zapret2"} {
        if _,err:=os.Lstat(filepath.Join(opt,"etc/init.d",name));err==nil {
            report.InstalledServices=append(report.InstalledServices,name)
        }
    }
    if len(report.OtherEngines)>0 {report.Notes=append(report.Notes,"Обнаружены другие процессы обработчиков пакетов; проверьте, не перехватывают ли они тот же трафик")}
    if len(report.InstalledServices)>0 {report.Notes=append(report.Notes,"Обнаружены установленные службы других обходов; наличие файла не означает, что служба запущена")}
    if !report.ProcessListAvailable {report.Notes=append(report.Notes,"Список процессов прочитан не полностью; отсутствие конфликтов не подтверждено")}
    report.Notes=append(report.Notes,"Тип /opt — ориентир, а не подтверждение выбора встроенной памяти или USB. Файлы и сетевые правила не изменены.")
    return report
}

func (m *Manager) Preflight() RouterPreflight {
    own:=0
    if router,ok:=m.backend.(*Router);ok {
        router.mu.Lock()
        if router.cmd!=nil && router.cmd.Process!=nil {own=router.cmd.Process.Pid}
        router.mu.Unlock()
    }
    return passivePreflight("/proc","/opt",own)
}
