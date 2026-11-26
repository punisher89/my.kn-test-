package pilot
import "testing"
func TestRecentHistoryRetention(t *testing.T){
 r:=recentHistory([]AdaptiveHost{{Updated:399},{Updated:400},{Updated:999}},1000)
 if len(r)!=2||r[0].Updated!=400{t.Fatalf("unexpected %+v",r)}
}
func TestHistoryRetentionConfig(t *testing.T){
 c:=DefaultConfig();c.HistoryRetentionDays=366
 if c.Validate()==nil{t.Fatal("invalid retention accepted")}
}
