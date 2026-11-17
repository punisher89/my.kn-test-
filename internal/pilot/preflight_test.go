package pilot

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestRouterPreflightReadOnlyAndD2KConflict(t *testing.T) {
    root:=t.TempDir()
    proc:=filepath.Join(root,"proc");opt:=filepath.Join(root,"opt")
    if err:=os.MkdirAll(filepath.Join(proc,"17"),0700);err!=nil {t.Fatal(err)}
    if err:=os.MkdirAll(filepath.Join(proc,"18"),0700);err!=nil {t.Fatal(err)}
    if err:=os.MkdirAll(filepath.Join(proc,"19"),0700);err!=nil {t.Fatal(err)}
    os.MkdirAll(filepath.Join(opt,"etc/init.d"),0700)
    os.WriteFile(filepath.Join(proc,"17","comm"),[]byte("nfqws2\n"),0600)
    os.WriteFile(filepath.Join(proc,"18","comm"),[]byte("nfqws2\n"),0600)
    os.WriteFile(filepath.Join(proc,"19","comm"),[]byte("my-app\n"),0600)
    os.WriteFile(filepath.Join(proc,"mounts"),[]byte("ubi0:rootfs "+opt+" ubifs rw,relatime 0 0\n"),0600)
    os.WriteFile(filepath.Join(opt,"etc/init.d/S99d2k"),[]byte(""),0600)
    got:=passivePreflight(proc,opt,17)
    if !got.Checked || !got.ProcessListAvailable || got.StorageKind!="possibly_internal_nand" { t.Fatal(got)}
    if len(got.OtherEngines)!=1 || got.OtherEngines[0]!="nfqws2" { t.Fatal("Pilot own engine or ordinary processes incorrectly included",got.OtherEngines)}
    if len(got.InstalledServices)!=1 || got.InstalledServices[0]!="S99d2k" {t.Fatal(got.InstalledServices)}
    if got.SpaceFreeBytes==nil {t.Fatal("no disk observation")}
    got=passivePreflight(filepath.Join(root,"unavailable"),opt,0)
    if got.ProcessListAvailable {t.Fatal("missing /proc treated as no conflicts")}
    if !strings.Contains(strings.Join(got.Notes,";"),"не полностью") {t.Fatal(got.Notes)}
}

func TestRouterPreflightEngineExactMatching(t *testing.T) {
    for _,n:=range []string{"nfqws","nfqws2","nfqws2-helper","zapret","d2k"} {if !knownEngineName(n){t.Fatal(n)}}
    for _,n:=range []string{"nfqws2other","random","bash","nfqws2not"} {if knownEngineName(n){t.Fatal(n)}}
}
