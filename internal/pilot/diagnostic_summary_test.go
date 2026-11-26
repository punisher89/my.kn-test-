package pilot

import "testing"

func TestDiagnosticHistoryWindowBounds(t *testing.T) {
	const now int64 = 1000000
	rows:=[]DiagnosticObservation{
		{Status:"failed",EventCount:100,FirstUpdated:now-7200,Updated:now-10},
		{Status:"selected",EventCount:5,FirstUpdated:now-60,Updated:now-1},
		{Status:"failed",EventCount:3,FirstUpdated:now-90000,Updated:now-88000},
	}
	s:=summarizeDiagnosticHistory(rows,now)
	if s.Rows!=3 || s.Events!=108 || s.FailedAttempts!=103 {t.Fatalf("totals: %+v",s)}
	if w:=s.Windows[0]; w.ConfirmedEvents!=6 || w.PossibleEvents!=104 || w.MixedRows!=1 {t.Fatalf("hour: %+v",w)}
	if w:=s.Windows[1]; w.ConfirmedEvents!=105 || w.PossibleEvents!=105 || w.MixedRows!=0 {t.Fatalf("day: %+v",w)}
	if w:=s.Windows[2]; w.ConfirmedEvents!=108 || w.PossibleEvents!=108 {t.Fatalf("week: %+v",w)}
}

func TestDiagnosticHealthFindingsReadOnly(t *testing.T) {
	const now int64=1000000
	free:=uint64(1)
	d:=DiagnosticReport{
		Service:DiagnosticService{Enabled:true},
		Current:[]DiagnosticObservation{{Updated:now-90000}},
		Storage:[]DiagnosticSlot{{State:"valid"},{State:"invalid_or_incomplete"}},
		ActiveSearch:&DiagnosticActiveSearch{Reason:"client_hello_missing"},
		Preflight:RouterPreflight{SpaceFreeBytes:&free},
	}
	want:=map[string]bool{"enabled_service_not_running":true,"observation_slot_needs_review":true,"no_recent_saved_observations":true,"active_search_client_hello_missing":true,"low_opt_free_space":true}
	for _,code:=range diagnosticHealthFindings(d,now){if !want[code]{t.Fatalf("unexpected %s",code)};delete(want,code)}
	if len(want)!=0 {t.Fatalf("missing: %v",want)}
	d.Service.Running=true
	d.Current[0].Updated=now
	d.Storage[1].State="valid"
	d.ActiveSearch=nil
	d.Preflight.SpaceFreeBytes=nil
	if got:=diagnosticHealthFindings(d,now);len(got)!=0{t.Fatalf("healthy snapshot: %v",got)}
}
