package pilot

import (
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
)

type settingsBackup struct {
 Schema int `json:"schema"`
 Version string `json:"version"`
 Config Config `json:"config"`
 SHA256 string `json:"sha256"`
}
func settingsDigest(c Config) string {
 raw,_:=json.Marshal(c)
 digest:=sha256.Sum256(raw)
 return hex.EncodeToString(digest[:])
}
func (m *Manager) SettingsBackup() (settingsBackup,error) {
 if !m.op.TryLock(){return settingsBackup{},ErrBusy};defer m.op.Unlock()
 c:=m.Config()
 return settingsBackup{Schema:1,Version:Version,Config:c,SHA256:settingsDigest(c)},nil
}
func (m *Manager) RestoreSettingsBackup(b settingsBackup) error {
 if b.Schema!=1||b.SHA256!=settingsDigest(b.Config){return errors.New("некорректная резервная копия настроек")}
 if !m.op.TryLock(){return ErrBusy};defer m.op.Unlock()
 old:=m.Config()
 // Never restore machine-specific paths, listener, queue, enable state or catalog.
 c:=b.Config
 c.Listen,c.EngineDir,c.RuntimeDir,c.Queue=old.Listen,old.EngineDir,old.RuntimeDir,old.Queue
 c.Enabled=old.Enabled
 c.Catalog,c.CatalogRevision=old.Catalog,old.CatalogRevision
 if err:=c.Validate();err!=nil{return err}
 return m.changeLocked(c)
}
