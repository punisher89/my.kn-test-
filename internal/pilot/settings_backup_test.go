package pilot
import "testing"
func TestSettingsBackupDigest(t *testing.T){
 c:=DefaultConfig()
 first:=settingsDigest(c)
 if len(first)!=64||first!=settingsDigest(c){t.Fatal("unstable digest")}
 c.Lightweight=!c.Lightweight
 if first==settingsDigest(c){t.Fatal("changed settings not detected")}
}
