package pilot

import (
 "reflect"
 "testing"
)

func TestFastStrategyLookupMatchesFullCatalog(t *testing.T) {
 c:=DefaultConfig()
 c.Catalog=[]Strategy{{ID:"test-catalog",Actions:[]string{"fake:example"}},{ID:"none",Actions:[]string{"fake:shadow"}}}
 for _,expected:=range StrategiesFor(c) {
  if expected.ID=="none" && len(expected.Actions)>0 {continue}
  got,ok:=strategyFor(c,expected.ID)
  if !ok||!reflect.DeepEqual(got,expected){t.Fatalf("lookup changed %s: %+v vs %+v",expected.ID,got,expected)}
 }
 if _,ok:=strategyFor(c,"unknown-method-test");ok{t.Fatal("unknown method accepted")}
 if n:=testing.AllocsPerRun(100,func(){_,_=strategyFor(c,"test-catalog")});n!=0{t.Fatalf("lookup allocates: %v",n)}
}
func TestObservationProtocolMatchesBoxContext(t *testing.T){
 for _,group:=range []string{"youtube_split_p443_tls13_ipv4","test_tls12_ipv6","test_legacy_ipv4","test_unknown_ipv6","unrecognized"} {
  tls,family:=observationProtocol(group)
  box:=boxContext(AdaptiveHost{Group:group})
  if box.TLS!=tls||box.Family!=family{t.Fatal(group,box)}
 }
}
func BenchmarkStrategyLookup(b *testing.B){
 c:=DefaultConfig()
 b.Run("old-full-catalog",func(b *testing.B){b.ReportAllocs();for i:=0;i<b.N;i++{for _,s:=range StrategiesFor(c){if s.ID=="fakedsplit-ts-11"{break}}}})
 b.Run("direct",func(b *testing.B){b.ReportAllocs();for i:=0;i<b.N;i++{_,_=strategyFor(c,"fakedsplit-ts-11")}})
}
