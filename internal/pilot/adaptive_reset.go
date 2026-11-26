package pilot

import (
	"bytes"
 "encoding/json"
 "time"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func adaptiveFrame(generation uint64, hosts, events []AdaptiveHost) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "PILOT4\t%d\n", generation)
	row := func(kind string, r AdaptiveHost) {
		hold := r.HoldReason
		if hold == "" {
			hold = "none"
		}
		count, first := r.EventCount, r.FirstUpdated
		if count < 1 {
			count = 1
			first = r.Updated
		}
		if kind == "R" {
			count = 1
			first = r.Updated
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%d\t%s\t%d\t%d\t%d\t%d\t%s\t%d\t%d\n", kind, r.Group, r.Host, r.Strategy, r.Status, r.Updated, r.Symptom, r.Network, r.Port, r.Origin, r.LastSwitch, r.WindowStart, r.Switches, r.HoldUntil, hold, count, first)
	}
	for _, r := range hosts {
		row("R", r)
	}
	for _, r := range events {
		row("E", r)
	}
	fmt.Fprintf(&b, "END\t%d\n", generation)
	return b.Bytes()
}

// Only called while the controller's operation lock is held and the packet
// engine is stopped. Atomic replacement retains its unprivileged ownership.
func writeAdaptiveFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pilot-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil && os.Geteuid() == 0 {
		err = f.Chown(adaptiveUID, adaptiveUID)
	}
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (m *Manager) resetDomainLocked(c Config, host string) error {
 return m.rewriteAdaptiveLocked(c, host, false)
}

func (m *Manager) rewriteAdaptiveLocked(c Config, host string, historyOnly bool, restore ...bool) (result error) {
 restoring:=len(restore)>0&&restore[0]
 if m.updateRequested.Load(){return ErrBusy}
	if _, err := os.Lstat(c.RuntimeDir); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := PrepareAdaptive(c); err != nil {
		return err
	}
	running := m.backend.Running()
	backups := map[string][]byte{}
	modified := false
	defer func() {
		if result != nil {
			if modified {
				for name, data := range backups {
					result = errors.Join(result, writeAdaptiveFile(filepath.Join(c.RuntimeDir, name), data))
				}
			}
			if running {
				result = errors.Join(result, m.backend.Start(c))
			}
		}
		m.setError(result)
	}()
	if running {
		if err := m.backend.Stop(); err != nil {
			return err
		}
	}
	// Snapshot only after stopping the writer. Reads and writes stay bounded.
	for _, name := range []string{"state0.tsv", "state1.tsv", "autohosts.txt"} {
		f, err := os.Open(filepath.Join(c.RuntimeDir, name))
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, 524289))
		f.Close()
		if err != nil {
			return err
		}
		if len(data) > 524288 {
			return errors.New("слишком большой файл опыта; сброс не выполнен")
		}
		backups[name] = data
	}
	snapshot := readAdaptiveState(c)
	if snapshot.Generation == 0 && (len(backups["state0.tsv"]) > 0 || len(backups["state1.tsv"]) > 0) {
		return errors.New("нет завершённого кадра опыта; отказ стирать остальные наблюдения")
	}
	if snapshot.Generation >= (1<<53)-2 {
		return errors.New("исчерпан счётчик кадров опыта")
	}
	if restoring {
  saved,err:=loadCleanupBackup(filepath.Join(filepath.Dir(m.path),"history-before-cleanup.json"))
  if err!=nil{return err}
  // The current state is itself retained before restoring earlier observations.
  if err:=WriteJSON(filepath.Join(filepath.Dir(m.path),"history-before-restore.json"),backups);err!=nil{return err}
  saved.Generation=snapshot.Generation
  snapshot=saved
 }
 hosts := snapshot.Hosts
 if !historyOnly {hosts = slices.DeleteFunc(hosts, func(r AdaptiveHost) bool { return host == "" || r.Host == host })}
 if historyOnly && !restoring {
  // Save both complete original slots, not just the interpreted history.
  // Fixed private file; retain the last pre-cleanup backup without unbounded growth.
  if err := WriteJSON(filepath.Join(filepath.Dir(m.path),"history-before-cleanup.json"), backups); err != nil {return err}
 }
	events := snapshot.History
 if !restoring {
  if historyOnly {events=recentHistory(events,time.Now().Unix())} else {events=slices.DeleteFunc(events,func(r AdaptiveHost)bool{return host==""||r.Host==host})}
 }
	discovered := []string{}
	for _, line := range strings.Split(string(backups["autohosts.txt"]), "\n") {
		if (historyOnly || host != "") && ValidDomain(line) && line != host && !slices.Contains(discovered, line) && len(discovered) < 1000 {
			discovered = append(discovered, line)
		}
	}
	files := map[string][]byte{
		"state0.tsv":    adaptiveFrame(snapshot.Generation+1, hosts, events),
		"state1.tsv":    adaptiveFrame(snapshot.Generation+2, hosts, events),
		"autohosts.txt": []byte(strings.Join(discovered, "\n")),
	}
	if len(discovered) > 0 {
		files["autohosts.txt"] = append(files["autohosts.txt"], '\n')
	}
	modified = true
	for _, name := range []string{"state0.tsv", "state1.tsv", "autohosts.txt"} {
		if err := writeAdaptiveFile(filepath.Join(c.RuntimeDir, name), files[name]); err != nil {
			return err
		}
	}
	if running {
		if err := m.backend.Start(c); err != nil {
			return err
		}
	}
	if restoring {
  m.log.Add("Восстановлены наблюдения и история из последней резервной копии; настройки не изменены")
 } else if historyOnly {
  m.log.Add("Старая история попыток очищена; свежие события, наблюдения и ручные правила сохранены")
 } else if host == "" {
		m.log.Add("Обучение всех доменов сброшено; ручные правила сохранены")
	} else {
		m.log.Add("Обучение домена сброшено: " + host + "; ручные правила сохранены")
	}
	return nil
}

func recentHistory(events []AdaptiveHost,now int64)[]AdaptiveHost{
 return slices.DeleteFunc(events,func(r AdaptiveHost)bool{return r.Updated<now-600})
}

// Only the daemon's own bounded local backup is accepted. Validate complete
// frames using the same parser as the live observation reader.
func loadCleanupBackup(path string)(AdaptiveSnapshot,error){
 f,err:=os.Open(path);if err!=nil{return AdaptiveSnapshot{},errors.New("резервная копия истории недоступна")};defer f.Close()
 info,err:=f.Stat();if err!=nil||!info.Mode().IsRegular()||info.Mode().Perm()&0077!=0{return AdaptiveSnapshot{},errors.New("небезопасная резервная копия истории")}
 data,err:=io.ReadAll(io.LimitReader(f,2200001));if err!=nil||len(data)>2200000{return AdaptiveSnapshot{},errors.New("резервная копия истории слишком большая")}
 var slots map[string][]byte
 if json.Unmarshal(data,&slots)!=nil{return AdaptiveSnapshot{},errors.New("повреждена резервная копия истории")}
 result:=AdaptiveSnapshot{}
 for _,name:=range []string{"state0.tsv","state1.tsv"}{
  raw:=slots[name];if len(raw)==0||len(raw)>524288{continue}
  tmp,err:=os.CreateTemp(filepath.Dir(path),".pilot-restore-*");if err!=nil{return result,err}
  n:=tmp.Name();_,err=tmp.Write(raw);closeErr:=tmp.Close()
  if err==nil{err=closeErr}
  if err==nil{generation,hosts,events,e:=readAdaptiveSlot(n);if e==nil&&generation>result.Generation{result.Generation=generation;result.Hosts=hosts;result.History=events}}
  _=os.Remove(n)
 }
 if result.Generation==0{return result,errors.New("нет корректного кадра в резервной копии")}
 return result,nil
}
