package meshstate

import (
 "encoding/json"
 "errors"
 "path/filepath"
 "testing"
 "time"
)

// Simulate the uncertain-durability state set after a failed directory sync.
func TestPoisonedJobsRejectStaleState(t *testing.T) {
 s, err := OpenLocalJobs(filepath.Join(t.TempDir(), "jobs.json"))
 if err != nil { t.Fatal(err) }
 if _, err = s.Accept("cancelled", json.RawMessage(`null`)); err != nil { t.Fatal(err) }
 if _, err = s.Cancel("cancelled"); err != nil { t.Fatal(err) }
 if _, err = s.Accept("running", json.RawMessage(`null`)); err != nil { t.Fatal(err) }
 now := time.Unix(100, 0)
 j, err := s.Claim("running", "owner", now, time.Minute)
 if err != nil { t.Fatal(err) }
 s.poisoned = true
 cases := []struct{name string; run func() (Job,error)}{
  {"cancel replay", func()(Job,error){return s.Cancel("cancelled")}},
  {"claim unknown", func()(Job,error){return s.Claim("missing","owner",now,time.Minute)}},
  {"renew stale", func()(Job,error){return s.Renew("running","wrong",j.Fence,now,time.Minute)}},
  {"checkpoint", func()(Job,error){return s.Checkpoint("running","owner",j.Fence,now,json.RawMessage(`null`))}},
  {"finish", func()(Job,error){return s.Finish("running","owner",j.Fence,now,Completed,json.RawMessage(`null`))}},
 }
 for _,tc := range cases {t.Run(tc.name,func(t *testing.T){got,err:=tc.run();if !errors.Is(err,ErrUnavailable)||got.ID!=""{t.Fatalf("uncertain store exposed stale state: %+v, %v",got,err)}})}
}
