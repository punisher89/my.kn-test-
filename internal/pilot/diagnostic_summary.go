package pilot

// Grouped history only retains the first and last timestamps, not each event.
type DiagnosticHistoryWindow struct {
	Seconds int64 `json:"seconds"`
	ConfirmedEvents int64 `json:"confirmed_events"`
	PossibleEvents int64 `json:"possible_events"`
	MixedRows int `json:"mixed_rows"`
}
type DiagnosticHistorySummary struct {
	Rows int `json:"rows"`
	Events int64 `json:"events"`
	FailedAttempts int64 `json:"failed_attempts"`
	Windows []DiagnosticHistoryWindow `json:"windows"`
}

func summarizeDiagnosticHistory(rows []DiagnosticObservation, now int64) DiagnosticHistorySummary {
	s := DiagnosticHistorySummary{Rows:len(rows), Windows:[]DiagnosticHistoryWindow{{Seconds:3600},{Seconds:86400},{Seconds:604800}}}
	for _, r := range rows {
		count:=r.EventCount
		if count<1 { count=1 }
		s.Events+=count
		if r.Status=="failed" { s.FailedAttempts+=count }
		for i:=range s.Windows {
			w:=&s.Windows[i]
			cutoff:=now-w.Seconds
			if r.Updated<=0 || r.Updated>now || r.Updated<cutoff { continue }
			w.PossibleEvents+=count
			if count==1 || (r.FirstUpdated>=cutoff && r.FirstUpdated<=r.Updated) {
				w.ConfirmedEvents+=count
			} else {
				w.ConfirmedEvents++
				w.MixedRows++
				if r.FirstUpdated>0 && r.FirstUpdated<cutoff {w.PossibleEvents--}
			}
		}
	}
	return s
}

// Read-only fixed codes. No arbitrary errors, secrets or network probes.
func diagnosticHealthFindings(d DiagnosticReport, now int64) []string {
	findings:=[]string{}
	if d.Service.Enabled && !d.Service.Running { findings=append(findings,"enabled_service_not_running") }
	if d.Service.HasError { findings=append(findings,"service_reported_error") }
	if d.ObservationReadError { findings=append(findings,"observation_read_error") }
	for _, slot:=range d.Storage {
		if slot.State=="unreadable" || slot.State=="invalid_or_incomplete" {
			findings=append(findings,"observation_slot_needs_review")
			break
		}
	}
	if len(d.Current)>0 {
		fresh:=false
		for _,r:=range d.Current { if r.Updated>=now-86400 && r.Updated<=now {fresh=true;break} }
		if !fresh {findings=append(findings,"no_recent_saved_observations")}
	}
	if d.ActiveSearch!=nil && d.ActiveSearch.Reason=="client_hello_missing" {
		findings=append(findings,"active_search_client_hello_missing")
	}
	if d.Preflight.SpaceFreeBytes!=nil && *d.Preflight.SpaceFreeBytes<32*1024*1024 {findings=append(findings,"low_opt_free_space")}
	if len(d.Preflight.OtherEngines)>0 {findings=append(findings,"other_packet_engines_detected")}
	return findings
}
